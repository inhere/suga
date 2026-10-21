"""Loader reloads the configuration; a reload keeps the keys."""

import os

MAX_KEYS = 10


class Loader:
    # reload keeps the keys
    reload_count = 0

    def __init__(self, path):
        self.path = path

    def reload(self):
        note = "reload kept the keys"
        self.reload_count += 1
        return note

    class Nested:
        def reload(self):
            return None


def reload(path):
    return Loader(path).reload()
