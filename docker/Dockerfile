FROM alpine:latest

RUN \
	apk add --update \
		python \
		python-dev \
		py-pip \
		build-base \
	&& \
	pip install dumb-init && \
	apk del \
		python \
		python-dev \
		py-pip \
		build-base \
	&& \
	rm -rf /var/cache/apk/* && \
	:

COPY kronjob /usr/bin/

ENTRYPOINT ["dumb-init"]
CMD ["kronjob", "run"]
