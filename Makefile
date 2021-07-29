REPO=docker-dc-micro-release.dr.corp.adobe.com/porter2k8s
SHA=$$(git rev-parse --short HEAD)
DATE=$$(date +%Y%m%d)
VERSION=$$(cat VERSION)

clean:
	rm -rf vendor porter2k8s

base:
	docker build --pull -t $(REPO):base . -f Dockerfile-base

upload-base:
	docker push $(REPO):base

build-container:
	docker build --pull -t $(REPO):build . -f Dockerfile-build --build-arg SHA=$(SHA)
	docker create --name extract $(REPO):build
	docker cp extract:/go/src/git.corp.adobe.com/EchoSign/porter2k8s/porter2k8s ./
	docker rm -f extract
	docker build -t $(REPO):$(SHA)_$(DATE) .

upload-current:
	docker push $(REPO):$(SHA)_$(DATE)
	docker tag $(REPO):$(SHA)_$(DATE) $(REPO):latest
	docker push $(REPO):latest
	docker tag $(REPO):latest $(REPO):$(VERSION)
	docker push $(REPO):$(VERSION)

run-tests:
	docker build --pull -t $(REPO):build . -f Dockerfile-build
	docker run $(REPO):build go test -v ./...
	docker run $(REPO):build /bin/bash test/test.sh
