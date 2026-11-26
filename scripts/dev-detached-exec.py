#!/usr/bin/env python3
"""Detach one launcher-owned service while preserving its PID and environment."""
import os
import signal
import sys

# The shell backgrounds this helper, so it is not its parent's group leader.
# Unlike nohup alone, setsid removes the terminal and launcher group entirely.
os.setsid()
signal.signal(signal.SIGHUP, signal.SIG_IGN)
os.execvp(sys.argv[1], sys.argv[1:])
