# Smoke test infrastructure

This directory contains the internal container-only black-box test infrastructure:

- `Dockerfile` builds the shared HTTP source fixture and smoke-client image;
- `source.py` serves deterministic HTTP source responses and control endpoints;
- `smoke.py` executes the acceptance scenarios;
- `fixtures/source` contains the baseline HTTP source payloads.

Run the suite through the repository Make targets documented in [`docs/LOCAL_SMOKE_TESTING.md`](../../docs/LOCAL_SMOKE_TESTING.md).
