"""Python SDK for Flagnetic, self-hosted feature flags and experiments."""

from flagnetic._client import Client
from flagnetic._detail import Detail, Experiment, Stage, Status
from flagnetic._eval import Context
from flagnetic._events import Logger
from flagnetic._version import __version__

__all__ = [
    "Client",
    "Context",
    "Detail",
    "Experiment",
    "Logger",
    "Stage",
    "Status",
    "__version__",
]
