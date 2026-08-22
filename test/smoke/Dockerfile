FROM python:3.14-alpine@sha256:a1321512d6a287428c50dcdf2ab3857761127e03a23b1f648e9c1c0de59288f8

WORKDIR /smoke

COPY test/smoke/source.py test/smoke/smoke.py /smoke/
COPY test/smoke/fixtures/source/ /fixtures/

RUN addgroup -S -g 10002 smoke \
    && adduser -S -D -H -u 10002 -G smoke smoke

USER smoke

CMD ["python", "/smoke/smoke.py"]
