#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = ["pyte"]
# ///
"""Drive `tend tui` (or `tend fzf`) on the fixture dataset in a pseudo-terminal and print screens.

usage: scripts/tui-drive.py [--size 120x34] [--tend BIN] [--dir DIR] [--cmd tui] STEP...
STEP: a key (enter esc tab space up down left right backspace ctrl+X, or literal text),
      text:STRING, sleep:SECONDS, dump.  The last screen is always printed.
"""

import argparse
import os
import pty
import re
import select
import signal
import subprocess
import sys
import tempfile
import time
from pathlib import Path

import pyte

KEYS = {
    "enter": "\r", "esc": "\x1b", "tab": "\t", "shift+tab": "\x1b[Z", "space": " ", "backspace": "\x7f",
    "up": "\x1b[A", "down": "\x1b[B", "right": "\x1b[C", "left": "\x1b[D",
    "home": "\x1b[H", "end": "\x1b[F", "pgup": "\x1b[5~", "pgdown": "\x1b[6~",
}
# ⚠️ tend waits for answers to the background-colour and cursor-position queries before it draws
QUERIES = [(re.compile(rb"\x1b\]11;\?(\x07|\x1b\\)"), b"\x1b]11;rgb:0000/0000/0000\x1b\\"),
           (re.compile(rb"\x1b\[6n"), b"\x1b[1;1R")]
REPO = Path(__file__).resolve().parent.parent


class Screen(pyte.Screen):
    """pyte.Screen with CSI n S / CSI n T (scroll the scroll region, the cursor staying where it is) and a CSI n M
    that clears a line whose replacement is blank, which pyte's leaves stale."""

    def scroll_up(self, count=None, *_, **__) -> None:
        self.shift(self.region()[0], count or 1)

    def scroll_down(self, count=None, *_, **__) -> None:
        self.shift(self.region()[0], -(count or 1))

    def delete_lines(self, count=None) -> None:
        top, bottom = self.region()
        if top <= self.cursor.y <= bottom:
            self.shift(self.cursor.y, count or 1)
            self.carriage_return()

    def region(self):
        return self.margins or pyte.screens.Margins(0, self.lines - 1)

    def shift(self, top: int, n: int) -> None:
        """Moves the lines from top to the region's bottom up by n (down when negative), blank lines filling in."""
        bottom = self.region()[1]
        rows = [self.buffer.pop(y, None) for y in range(top, bottom + 1)]
        n = max(-len(rows), min(n, len(rows)))
        rows = rows[n:] + [None] * n if n >= 0 else [None] * -n + rows[:n]
        for y, row in zip(range(top, bottom + 1), rows):
            if row is not None:
                self.buffer[y] = row
        self.dirty.update(range(top, bottom + 1))


class Stream(pyte.ByteStream):
    csi = {**pyte.ByteStream.csi, "S": "scroll_up", "T": "scroll_down"}


def key(step: str) -> str:
    if step in KEYS:
        return KEYS[step]
    if step.startswith("ctrl+") and len(step) == 6:
        return chr(ord(step[5].lower()) & 0x1F)
    return step


def dataset(args) -> Path:
    if args.dir and (Path(args.dir) / "tend.sh").exists():
        root = Path(args.dir)
        if not (root / "stubs").is_dir():
            sys.exit(f"{root}: no stubs/, made by an older tools/fixture; remove it and run again")
        return root
    # ⚠️ not under an agent scratch dir (claude-… in temp): the scanner hides every session there
    base = Path.home() / ".cache" / "tend-tui-drive"
    base.mkdir(parents=True, exist_ok=True)
    root = Path(args.dir) if args.dir else Path(tempfile.mkdtemp(dir=base)) / "data"
    subprocess.run(["go", "run", "./tools/fixture", "-o", str(root), "-tend", args.tend],
                   cwd=REPO, check=True, stdout=subprocess.DEVNULL)
    return root


def environment() -> dict:
    env = {k: v for k, v in os.environ.items()
           if not k.startswith(("HERDR_", "CLAUDE_CODE_", "CODEX_", "TEND_"))}
    env["TERM"] = "xterm-256color"
    return env


def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("--size", default="120x34")
    p.add_argument("--tend", default=str(Path.home() / ".local" / "bin" / "tend"))
    p.add_argument("--dir", help="reuse or create the fixture dataset here")
    p.add_argument("--cmd", default="tui")
    p.add_argument("steps", nargs="*")
    args = p.parse_args()
    cols, rows = map(int, args.size.split("x"))
    root = dataset(args)
    print(f"dataset: {root}", file=sys.stderr)

    screen = Screen(cols, rows)
    stream = Stream(screen)
    pid, fd = pty.fork()
    if pid == 0:
        import fcntl, struct, termios
        fcntl.ioctl(0, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))
        os.execve(str(root / "tend.sh"), [str(root / "tend.sh"), args.cmd], environment())

    def pump(seconds: float) -> None:
        end = time.time() + seconds
        while time.time() < end:
            r, _, _ = select.select([fd], [], [], max(0.0, end - time.time()))
            if not r:
                continue
            try:
                data = os.read(fd, 65536)
            except OSError:
                return
            for pat, answer in QUERIES:
                if pat.search(data):
                    os.write(fd, answer)
            stream.feed(data)

    def dump() -> None:
        print("\n".join(line.rstrip() for line in screen.display))
        print("-" * cols)

    pump(1.5)
    for step in args.steps:
        if step == "dump":
            dump()
        elif step.startswith("sleep:"):
            pump(float(step[6:]))
        elif step.startswith("text:"):
            os.write(fd, step[5:].encode())
            pump(0.4)
        else:
            os.write(fd, key(step).encode())
            pump(0.4)
    dump()
    os.kill(pid, signal.SIGTERM)
    for _ in range(20):
        pump(0.1)
        if os.waitpid(pid, os.WNOHANG)[0]:
            return 0
    os.kill(pid, signal.SIGKILL)
    os.waitpid(pid, 0)
    return 0


if __name__ == "__main__":
    sys.exit(main())
