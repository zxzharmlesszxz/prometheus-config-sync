FROM python:3.14-alpine@sha256:05b2b8b732ecd268fee8727a369f936f022d1321b59befd13c30ede22769dcdc

WORKDIR /smoke

COPY test/smoke/source.py test/smoke/smoke.py /smoke/
COPY test/smoke/fixtures/source/ /fixtures/

RUN addgroup -S -g 10002 smoke \
    && adduser -S -D -H -u 10002 -G smoke smoke

USER smoke

CMD ["python", "/smoke/smoke.py"]
