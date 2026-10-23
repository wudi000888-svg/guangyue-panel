#!/usr/bin/env python3
"""Run every SQLite backend test, retaining Go's timeout for each process."""
import argparse
import os
import re
import subprocess


def plan(go_bin, env):
    packages = subprocess.check_output([go_bin, "list", "./..."], env=env, text=True).splitlines()
    targets = [name for name in packages if name.endswith("/internal/controlplane")]
    if len(targets) != 1:
        raise RuntimeError("expected exactly one controlplane package")
    target = targets[0]
    listed = subprocess.check_output([go_bin, "test", "-race", target, "-list", "."], env=env, text=True)
    names = [line for line in listed.splitlines() if re.fullmatch(r"(?:Test|Example|Fuzz)\w*", line)]
    if not names or len(names) != len(set(names)):
        raise RuntimeError("empty or ambiguous controlplane test list")
    shards = [names[index::3] for index in range(3)]
    flattened = [name for shard in shards for name in shard]
    if len(flattened) != len(names) or set(flattened) != set(names):
        raise RuntimeError("controlplane shards must cover every listed test exactly once")
    return target, [name for name in packages if name != target], shards


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("go_bin")
    parser.add_argument("--list-only", action="store_true")
    parser.add_argument(
        "--shard",
        type=int,
        choices=range(1, 4),
        help="run only this controlplane shard (CI runs the three in parallel jobs); shard 1 also runs the other packages",
    )
    args = parser.parse_args()
    env = dict(os.environ, GY_TEST_POSTGRES_DSN="")
    target, others, shards = plan(args.go_bin, env)
    print(f"SQLite controlplane coverage: {sum(map(len, shards))} tests in {len(shards)} serial shards", flush=True)
    for index, shard in enumerate(shards, 1):
        print(f"Shard {index}: {len(shard)} tests: " + ", ".join(shard), flush=True)
    if args.list_only:
        return
    if others and args.shard in (None, 1):
        subprocess.run([args.go_bin, "test", "-race", *others, "-count=1"], env=env, check=True)
    # Serial execution keeps shared Redis fixtures isolated. Every top-level
    # test retains all its subtests, race checking and Go's default 10m timeout.
    # A parallel CI shard runs on its own runner with its own Redis service.
    for index, shard in enumerate(shards, 1):
        if args.shard is not None and index != args.shard:
            continue
        if not shard:
            continue
        expression = "^(" + "|".join(map(re.escape, shard)) + ")$"
        print(f"Running SQLite controlplane shard {index}/{len(shards)}", flush=True)
        subprocess.run([args.go_bin, "test", "-race", target, "-run", expression, "-count=1"], env=env, check=True)


if __name__ == "__main__":
    main()
