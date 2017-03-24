FROM alpine:latest

EXPOSE 9102

COPY kronjob /usr/bin/

RUN /usr/bin/kronjob version

ENTRYPOINT ["kronjob"]
CMD ["run", "-v"]
