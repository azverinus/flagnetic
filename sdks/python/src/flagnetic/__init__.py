"""Python SDK for Flagnetic, self-hosted feature flags and experiments."""

from flagnetic._client import Client, Document
from flagnetic._detail import Detail, Experiment, Stage, Status
from flagnetic._eval import Context
from flagnetic._events import Logger
from flagnetic._overrides import LocalOverrideRefused, LocalOverrides, LocalOverridesNotAllowed
from flagnetic._reporting import Alias, Exposure, Recorder, SummaryCount
from flagnetic._version import __version__

__all__ = [
    "Alias",
    "Client",
    "Context",
    "Detail",
    "Document",
    "Experiment",
    "Exposure",
    "LocalOverrideRefused",
    "LocalOverrides",
    "LocalOverridesNotAllowed",
    "Logger",
    "Recorder",
    "Stage",
    "Status",
    "SummaryCount",
    "__version__",
]
