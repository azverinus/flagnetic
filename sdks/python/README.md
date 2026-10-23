# flagnetic

Python SDK for [Flagnetic](https://github.com/azverinus/flagnetic), self-hosted feature flags and experiments.

```bash
pip install flagnetic
```

It needs Python 3.11 or later and installs no dependency.

## Development

```bash
uv sync
uv run ruff check . && uv run ruff format --check . && uv run mypy
uv run pytest
```

The tests run the shared conformance suite in `conformance/` against the evaluator, and measure that evaluating one flag of a 1,000-flag ruleset stays under a millisecond at the 99th percentile.
