FROM python:3.15-alpine@sha256:0f9552bb28711333da16915ec146f5e6bb44a7fdac9ced15dc17c2e0206346dd

WORKDIR /smoke

COPY test/smoke/source.py test/smoke/smoke.py /smoke/
COPY test/smoke/fixtures/source/ /fixtures/

RUN addgroup -S -g 10002 smoke \
    && adduser -S -D -H -u 10002 -G smoke smoke

USER smoke

CMD ["python", "/smoke/smoke.py"]
