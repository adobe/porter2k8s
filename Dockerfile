From alpine

ADD porter2k8s /usr/local/bin/porter2k8s

RUN apk --no-cache add ca-certificates curl python3
RUN curl https://bootstrap.pypa.io/get-pip.py | python3 && pip install awscli

ENTRYPOINT [ "porter2k8s" ]
