#!/usr/bin/env python3
"""End-to-end smoke test for first-run onboarding and offline CLI commands.

The test drives the real binary in an isolated worktree-local scratch directory.
It checks that version/help are pure queries, ``init`` creates private commented
config and env templates, repeated init preserves both files, ``--force`` backs
up only config, explicit ``SUGA_CONFIG`` is protected, and config/path/models
commands keep stdout and stderr useful for callers. The model fixture uses a
closed localhost endpoint: ``models`` must finish without constructing a
provider or making a request.

The chat candidate/Tab/trace and config-file snapshot checks are in
``chat_model.py``, which runs immediately after this scenario from
``make smoke-chat``.

Reverse verification (Linux/macOS only):

* temporarily call ``EnsureHome`` before the version/help early return in
  ``cmd/suga/main.go``; this script must fail at the no-side-effect checks;
* temporarily remove the `--force` backup or the existing-file double check;
  the force/preservation assertions must fail;
* temporarily make ``models`` build a runtime provider or remove a referenced
  fallback from the catalog; the closed-endpoint/offline or JSON assertions
  must fail;
* restore the change before running the normal scenario. The supervisor should
  run both the modified and restored versions; this Windows host has no PTY.

Run on Linux/macOS through ``make smoke-chat`` or directly with
``python3 -I scripts/ptysmoke/chat_onboarding.py --bin tmp/smoke/suga``.
"""

import argparse
import json
import os
import shutil
import stat
import subprocess
from pathlib import Path


def command(binary, args, env, cwd, timeout=30):
    """Run one CLI command while retaining stdout/stderr and its exit code."""
    return subprocess.run(
        [binary] + args,
        cwd=str(cwd),
        env=env,
        capture_output=True,
        text=True,
        timeout=timeout,
    )


def fail(message, result=None):
    """Raise an assertion with command output, preserving both streams."""
    if result is not None:
        message += (
            f"\nexit={result.returncode}\n"
            f"stdout:\n{result.stdout}\n"
            f"stderr:\n{result.stderr}"
        )
    raise AssertionError(message)


def expect(result, code=0, stdout=None, stderr=None):
    """Assert an exit code and optional substrings in the separate streams."""
    if result.returncode != code:
        fail("unexpected exit code", result)
    if stdout:
        for text in stdout:
            if text not in result.stdout:
                fail(f"stdout does not contain {text!r}", result)
    if stderr:
        for text in stderr:
            if text not in result.stderr:
                fail(f"stderr does not contain {text!r}", result)


def clean_env(home, explicit=None):
    """Build an isolated environment without inheriting the user's config path.

    The checked words are Chinese, and this scenario starts from a home without
    config.yaml (the first run creates it), so the language is pinned through
    the environment: SUGA_LANGUAGE wins over `language:` in the file for a real
    user too, and the scenarios that do have a home pin it in the config.
    """
    env = dict(os.environ)
    env["SUGA_HOME"] = str(home)
    env["SUGA_LANGUAGE"] = "zh"
    env.pop("SUGA_MODEL", None)
    if explicit is None:
        env.pop("SUGA_CONFIG", None)
    else:
        env["SUGA_CONFIG"] = str(explicit)
    env["SUGA_HOOKS"] = "off"
    return env


def assert_private(path, mode):
    """Check POSIX permission bits; Windows expresses this through ACLs."""
    if os.name != "nt":
        actual = stat.S_IMODE(path.stat().st_mode)
        if actual != mode:
            fail(f"{path} has mode {oct(actual)}, expected {oct(mode)}")


def assert_comment_only_env(path):
    """Reject non-comment assignment lines in the generated env template."""
    for line in path.read_text(encoding="utf-8").splitlines():
        stripped = line.strip()
        if stripped and not stripped.startswith("#"):
            fail(f"generated .env contains a value line: {line!r}")


def run_onboarding(binary, work):
    """Exercise fresh, repeated, forced, and explicit-source initialization."""
    home = work / "home"
    env = clean_env(home)
    ws = work / "workspace"
    ws.mkdir(parents=True)

    for args in (("version",), ("--help",)):
        result = command(binary, list(args), env, ws)
        expect(result)
        if home.exists():
            fail(f"{args[0]} created {home}")

    result = command(binary, ["init"], env, ws)
    expect(result, stdout=["已写入"], stderr=["已初始化 suga 配置"])
    config = home / "config.yaml"
    dotenv = home / ".env"
    if not config.is_file() or not dotenv.is_file():
        fail("init did not create config.yaml and .env")
    assert_private(home, 0o700)
    assert_private(config, 0o600)
    assert_private(dotenv, 0o600)
    if "model: deepseek/deepseek-chat" not in config.read_text(encoding="utf-8"):
        fail("onboarding config is missing its safe model")
    assert_comment_only_env(dotenv)
    config_before = config.read_bytes()
    env_before = dotenv.read_bytes()

    result = command(binary, ["init"], env, ws)
    expect(result, stdout=["未覆盖"])
    if config.read_bytes() != config_before or dotenv.read_bytes() != env_before:
        fail("repeated init changed an existing onboarding file")

    old_config = b"# user-owned config\nmodel: custom/old\n"
    old_env = b"PRIVATE_API_KEY=keep-me\n"
    config.write_bytes(old_config)
    dotenv.write_bytes(old_env)
    result = command(binary, ["init"], env, ws)
    expect(result, stdout=["未覆盖"])
    if config.read_bytes() != old_config or dotenv.read_bytes() != old_env:
        fail("init without --force changed an existing user file")
    result = command(binary, ["init", "--force"], env, ws)
    expect(result, stdout=["已备份", "已写入"])
    backups = sorted(home.glob("config.yaml.bak.*"))
    if len(backups) != 1 or backups[0].read_bytes() != old_config:
        fail("init --force did not create the exact timestamped config backup")
    if config.read_bytes() == old_config or dotenv.read_bytes() != old_env:
        fail("init --force changed the wrong files")

    external = work / "external.yaml"
    external.write_text("model: explicit/provider\n", encoding="utf-8")
    explicit_home = work / "explicit-home"
    explicit_env = clean_env(explicit_home, external)
    result = command(binary, ["init", "--force"], explicit_env, ws)
    expect(result, code=2, stderr=["SUGA_CONFIG is set"])
    if (explicit_home / "config.yaml").exists():
        fail("explicit SUGA_CONFIG mode created a home config")
    if external.read_text(encoding="utf-8") != "model: explicit/provider\n":
        fail("explicit SUGA_CONFIG was modified")
    if not (explicit_home / ".env").is_file():
        fail("explicit SUGA_CONFIG mode did not create the comment-only .env")
    result = command(binary, ["config", "path"], explicit_env, ws)
    expect(result, stdout=[f"config = {external}", "source = explicit"])


def run_config_and_models(binary, work):
    """Exercise real config leaves and the offline model projection."""
    home = work / "config-home"
    ws = work / "config-workspace"
    ws.mkdir(parents=True)
    env = clean_env(home)
    expect(command(binary, ["init"], env, ws), stdout=["已写入"])

    fixture = """# keep this comment\nmodel: fake/m1\ncompact_model: fake/compact\nfallback_models:\n  - fake/unlisted\nproviders:\n  fake:\n    type: openai\n    base_url: http://127.0.0.1:1/v1\n    models:\n      compact:\n        context_window: 64000\n      m1:\n        model_id: upstream-m1\n        context_window: 128000\n"""
    (home / "config.yaml").write_text(fixture, encoding="utf-8")

    result = command(binary, ["config", "path"], env, ws)
    expect(result, stdout=[f"home = {home}", f"config = {home / 'config.yaml'}", "source = home"])
    config_before_secret = (home / "config.yaml").read_bytes()
    result = command(binary, ["config", "set", "providers.fake.api_key", "do-not-write"], env, ws)
    expect(result, code=2, stderr=["secret"])
    if "do-not-write" in result.stdout or "do-not-write" in result.stderr:
        fail("secret config value was echoed")
    if (home / "config.yaml").read_bytes() != config_before_secret:
        fail("secret config rejection changed the file")
    result = command(binary, ["config", "set", "tools.allow_write", "true"], env, ws)
    expect(result, stdout=["已写入", "tools.allow_write = true", "source = config:"])
    if "# keep this comment" not in (home / "config.yaml").read_text(encoding="utf-8"):
        fail("config set did not preserve the fixture comment")
    expect(command(binary, ["config", "get", "tools.allow_write"], env, ws),
           stdout=["tools.allow_write = true", "source = config:"])
    expect(command(binary, ["config", "list"], env, ws), stdout=["model = fake/m1", "tools.allow_write = true"])
    expect(command(binary, ["config", "unset", "tools.allow_write"], env, ws),
           stdout=["已删除 tools.allow_write"])

    result = command(binary, ["models"], env, ws)
    expect(result, stdout=["Providers (offline)", "fake/unlisted", "No network request was made."])
    result = command(binary, ["models", "--json"], env, ws)
    expect(result)
    try:
        catalog = json.loads(result.stdout)
    except json.JSONDecodeError as exc:
        fail(f"models --json is not JSON: {exc}", result)
    if not catalog.get("offline"):
        fail("models --json did not declare offline=true")
    expected = {"fake/compact", "fake/m1", "fake/unlisted"}
    if set(catalog.get("switchable", [])) != expected:
        fail(f"unexpected switchable model set: {catalog.get('switchable')!r}")
    if catalog.get("source") != "config":
        fail(f"unexpected model catalog source: {catalog.get('source')!r}")


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--bin", required=True, help="path to the built suga binary")
    parser.add_argument("--work", required=True, help="worktree-local scratch directory")
    parser.add_argument("--keep", action="store_true", help="keep scratch files after the run")
    args = parser.parse_args()

    binary = os.path.abspath(args.bin)
    work = Path(os.path.abspath(args.work))
    shutil.rmtree(work, ignore_errors=True)
    work.mkdir(parents=True)
    try:
        run_onboarding(binary, work)
        run_config_and_models(binary, work)
        print("onboarding/config/models smoke: all checks passed")
        if os.name == "nt":
            print("UNVERIFIED_PTY_WINDOWS: PTY chat scenarios require Linux/macOS")
    finally:
        if not args.keep:
            shutil.rmtree(work, ignore_errors=True)


if __name__ == "__main__":
    try:
        main()
    except (AssertionError, OSError, subprocess.SubprocessError) as exc:
        print(f"failed: {exc}")
        raise SystemExit(1)
