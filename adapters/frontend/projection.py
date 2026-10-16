"""Exact-anchor source projection with auditable input and output digests."""
import hashlib
from pathlib import Path


class Projection:
    """Track each projected file and fail closed when upstream anchors drift."""

    def __init__(self, root):
        self.root = Path(root)
        self.files = {}

    def read(self, name):
        path = self.root / name
        if name not in self.files:
            self.files[name] = {"before": digest(path) if path.exists() else None}
        return path.read_text(encoding="utf-8")

    def write(self, name, value):
        path = self.root / name
        if name not in self.files:
            self.files[name] = {"before": digest(path) if path.exists() else None}
        path.parent.mkdir(parents=True, exist_ok=True)
        if isinstance(value, bytes):
            path.write_bytes(value)
        else:
            path.write_text(value, encoding="utf-8")
        self.files[name]["after"] = digest(path)

    def replace(self, name, old, new, count=1):
        value = self.read(name)
        if value.count(old) != count:
            raise ValueError(f"frontend source drift: {name}: expected {count} exact anchors")
        self.write(name, value.replace(old, new))


def digest(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()
