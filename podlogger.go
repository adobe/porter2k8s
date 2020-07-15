package porter2k8s

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

// PodLogStreaming struct used for streaming pod logs from K8S
type PodLogStreaming struct {
	watcher watch.Interface
	group   sync.WaitGroup
	cancel  context.CancelFunc
}

// PodLogSinker interface provides destination stream for pod logs
// See LogrusSink(), DirectoryLogSink()
type PodLogSinker interface {
	SinkForPod(podname string) (io.WriteCloser, error)
}

// StreamPodLogs starts watching for pods inside the K8s cluster and streaming log data to specified destination.
// Logs are retrieved for any pod matching the specified selector and that is not in phase 'pending'.
//
// ctx - context to cancel/timeout all operations
// client - K8S client PodInterface
// podselector - selector string for pod search
// containerName - container to retrieve logs from
// logDestination - callback for providing the log stream destination for a pod
func StreamPodLogs(
	ctx context.Context,
	client corev1.PodInterface,
	podselector string,
	containerName string,
	logDestination PodLogSinker) (*PodLogStreaming, error) {
	if logDestination == nil {
		return nil, nil
	}

	options := metav1.ListOptions{
		LabelSelector: podselector,
		FieldSelector: "status.phase!=Pending,status.phase!=Unknown",
	}
	watcher, err := client.Watch(options)
	if err != nil {
		return nil, err
	}

	// query and attach to pod logs in the background
	ctx, cancel := context.WithCancel(ctx)
	p := PodLogStreaming{watcher: watcher, cancel: cancel}
	p.group.Add(1)
	go func() {
		defer p.group.Done()

		watchCh := watcher.ResultChan()
		for {
			select {
			case <-ctx.Done():
				return

			case event, ok := <-watchCh:
				if !ok {
					return
				}

				switch event.Type {
				case watch.Added:
					pod, ok := event.Object.(*apiv1.Pod)
					if !ok {
						log.Warnf("%#v is not a pod event", event)
						return
					}

					p.group.Add(1)
					go func(podname string) {
						defer p.group.Done()

						log.Infof("Retrieving logs for pod %s", podname)

						err := attachToPod(ctx, podname, containerName, client, logDestination)
						switch err {
						case nil, io.EOF:
							log.Infof("End of logs for pod %s", podname)
						case context.Canceled:
							log.Debugf("Canceled retrieving logs for pod %s", podname)
						default:
							log.Warnf("Error consuming logs from pod %s: %v", podname, err)
						}
					}(pod.Name)

				case watch.Error:
					err := errors.FromObject(event.Object)
					log.Warnf("error reported while watching pods: %v", err)
				}
			}
		}
	}()
	return &p, nil
}

// Stop ends the pod watching and log streaming operations.
// gracePeriod - time to allow for any log streaming to finish.
func (p *PodLogStreaming) Stop(gracePeriod time.Duration) bool {
	if p == nil {
		return true
	}
	defer p.cancel() // ultimately reel in any runaway log-streaming-gophers after the graceperiod

	p.watcher.Stop() // stop the main pod-watching-gopher
	c := make(chan struct{})
	go func() {
		defer close(c)
		p.group.Wait()
	}()
	select {
	case <-c:
		return true
	case <-time.After(gracePeriod):
		return false // timed out
	}
}

func attachToPod(
	ctx context.Context, podname string, containerName string, client corev1.PodInterface, sinker PodLogSinker) error {
	podLogOptions := apiv1.PodLogOptions{
		Container: containerName,
		Follow:    true,
	}
	src, err := client.GetLogs(podname, &podLogOptions).Context(ctx).Stream()
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := sinker.SinkForPod(podname)
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	return err
}

type directoryLogSink struct {
	logDir string
}

func (dls directoryLogSink) SinkForPod(podname string) (io.WriteCloser, error) {
	path := filepath.Join(dls.logDir, podname+".log")
	return os.Create(path)
}

// DirectoryLogSink creates a new PodLogSinker that writes pod logs into the filesystem at the specified location.
func DirectoryLogSink(logDir string) PodLogSinker {
	return directoryLogSink{logDir: logDir}
}

// LogrusSink creates a new PodLogSinker that streams pod logs to the logrus std logger
func LogrusSink() PodLogSinker {
	return sinkForPodFunc(func(podname string) (io.WriteCloser, error) {
		return prefixedWriter(log.StandardLogger().Writer(), fmt.Sprintf("[%s]", podname)), nil
	})
}

type sinkForPodFunc func(podname string) (io.WriteCloser, error)

func (fn sinkForPodFunc) SinkForPod(podname string) (io.WriteCloser, error) {
	return fn(podname)
}

func prefixedWriter(dest io.Writer, prefix string) io.WriteCloser {
	reader, writer := io.Pipe()

	go func() {
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			line := fmt.Sprintf("%s %s\n", prefix, scanner.Bytes())
			if _, err := dest.Write([]byte(line)); err != nil {
				break
			}
		}
	}()
	return writer
}
