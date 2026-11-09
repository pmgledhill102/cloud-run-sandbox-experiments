#!/usr/bin/env python3
"""When does `sandbox do` exit after its command finishes, if the host keeps
stdin open? Settles a conflict between cold runs B3 and B4.

Runs on the host of a Cloud Run job with sandboxes enabled. For each case it
starts `sandbox do` with all three streams piped, waits for the command's
"done" line, then measures when stdout reaches EOF and when the CLI exits,
while still holding stdin open. If the CLI hasn't exited 20 s after "done",
it closes stdin and measures again. One JSON line per run, on stdout.
"""
import json
import os
import subprocess
import threading
import time

SANDBOX = "/usr/local/gcp/bin/sandbox"
WAIT = 20.0
REPS = 3
os.makedirs("/tmp/mnt", exist_ok=True)

DONE_SH = ["/bin/sh", "-c", "echo done"]
DONE_PY = ["/usr/bin/python3", "-c", "print('done', flush=True)"]
READ_PY = ["/usr/bin/python3", "-c", "import sys; sys.stdin.readline(); print('done', flush=True)"]

# name, sandbox flags, command, bytes to write to stdin before waiting
CASES = [
    ("sh-plain", [], DONE_SH, b""),
    ("py-plain", [], DONE_PY, b""),
    ("py-stdin-true", ["--stdin=true"], DONE_PY, b""),
    ("py-write", ["--write"], DONE_PY, b""),
    ("py-write-mount", ["--write", "--mount", "type=bind,src=/tmp/mnt,dst=/mnt"], DONE_PY, b""),
    ("py-rig-flags", ["--stdin=true", "--write", "--mount", "type=bind,src=/tmp/mnt,dst=/mnt",
                      "--env", "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"], DONE_PY, b""),
    ("py-unread-stdin", [], DONE_PY, b"x" * 65536),
    ("py-reads-a-line", [], READ_PY, b"line\n"),
    ("py-stdin-false", ["--stdin=false"], DONE_PY, b""),
]


def run(name, flags, cmd, feed, rep):
    t0 = time.monotonic()
    p = subprocess.Popen([SANDBOX, "do", *flags, "--", *cmd],
                         stdin=subprocess.PIPE if "--stdin=false" not in flags else subprocess.DEVNULL,
                         stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    marks = {}

    def read_out():
        for line in p.stdout:
            if b"done" in line and "done" not in marks:
                marks["done"] = time.monotonic()
        marks["stdout_eof"] = time.monotonic()

    threading.Thread(target=read_out, daemon=True).start()
    threading.Thread(target=lambda: p.stderr.read(), daemon=True).start()
    if feed and p.stdin:
        try:
            p.stdin.write(feed)
            p.stdin.flush()
        except BrokenPipeError:
            marks["feed_broken_pipe"] = True
    deadline = time.monotonic() + 60
    while "done" not in marks and time.monotonic() < deadline and p.poll() is None:
        time.sleep(0.001)
    t_done = marks.get("done")
    exited_open = None
    try:
        p.wait(timeout=WAIT)
        exited_open = time.monotonic()
    except subprocess.TimeoutExpired:
        pass
    closed_at = exit_after_close = None
    if exited_open is None:
        closed_at = time.monotonic()
        if p.stdin:
            p.stdin.close()
        try:
            p.wait(timeout=WAIT)
            exit_after_close = time.monotonic() - closed_at
        except subprocess.TimeoutExpired:
            p.kill()

    def rel(t):
        return None if t is None or t_done is None else round(t - t_done, 3)
    print(json.dumps({
        "component": "shutdown-probe", "case": name, "rep": rep, "rc": p.returncode,
        "start_to_done_s": None if t_done is None else round(t_done - t0, 3),
        "done_to_stdout_eof_s": rel(marks.get("stdout_eof")),
        "done_to_exit_with_stdin_open_s": rel(exited_open),
        "exit_after_closing_stdin_s": None if exit_after_close is None else round(exit_after_close, 3),
        "feed_broken_pipe": marks.get("feed_broken_pipe", False),
    }), flush=True)


print(json.dumps({"component": "shutdown-probe", "msg": "start",
                  "cli_sha256_prefix": subprocess.run(["sha256sum", SANDBOX], capture_output=True, text=True).stdout[:16]}),
      flush=True)
for rep in range(1, REPS + 1):
    for c in CASES:
        run(*c, rep)
print(json.dumps({"component": "shutdown-probe", "msg": "end"}), flush=True)
time.sleep(15)  # Cloud Logging loses lines from a container that exits straight after printing (B3)
