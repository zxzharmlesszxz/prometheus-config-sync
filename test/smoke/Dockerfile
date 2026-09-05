FROM python:3.14-alpine@sha256:c6ead215bfd31f1e433d968853b7a769989117115b728874824e6c0a27cb96fc

WORKDIR /smoke

COPY test/smoke/source.py test/smoke/smoke.py /smoke/
COPY test/smoke/fixtures/source/ /fixtures/

RUN addgroup -S -g 10002 smoke \
    && adduser -S -D -H -u 10002 -G smoke smoke

USER smoke

CMD ["python", "/smoke/smoke.py"]
