#!/usr/bin/env python3
"""Create or update the shutdown-probe job with probe.py inlined, and run it.
  PROJECT=<id> IMAGE=<image with python3> python3 run.py
The image is the sandbox-agent POC's (any image with python3 works)."""
import os, subprocess, sys, json
HERE = os.path.dirname(os.path.abspath(__file__))
P, IMG = os.environ["PROJECT"], os.environ["IMAGE"]
import yaml
job = yaml.safe_load(open(os.path.join(HERE, "job.yaml.tmpl")).read().replace("${PROJECT}", P).replace("${IMAGE}", IMG))
job["spec"]["template"]["spec"]["template"]["spec"]["containers"][0]["env"][0]["value"] = open(os.path.join(HERE, "probe.py")).read()
out = os.path.join("/tmp", "shutdown-probe-job.yaml")
yaml.safe_dump(job, open(out, "w"))
crun = os.path.join(HERE, "..", "sandbox-agent", "crun.py")
for args in (["apply", out], ["run", "shutdown-probe"]):
    r = subprocess.run([sys.executable, crun, *args], env={**os.environ, "PROJECT": P}, capture_output=True, text=True)
    print(r.stdout[-600:], r.stderr[-300:])
