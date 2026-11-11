#!/usr/bin/env python3
"""Plan/copy stopped gcs-webnora local storage into platform-user JFS layout.

Python 3.7+, standard library only. Never deletes source data or rewrites file
references. PostgreSQL migration 138 must be installed before --apply.
"""
import argparse
import hashlib
import json
import os
import pathlib
import stat
import subprocess
import time


def inspect(name):
    return json.loads(subprocess.check_output(["docker", "inspect", name], universal_newlines=True))[0]


def sql(query):
    env = dict(v.split("=", 1) for v in inspect("gcs-webnora-postgres-dev")["Config"]["Env"] if "=" in v)
    return subprocess.check_output(["docker", "exec", "gcs-webnora-postgres-dev", "psql", "-X", "-v", "ON_ERROR_STOP=1", "-U", env["POSTGRES_USER"], "-d", env["POSTGRES_DB"], "-At", "-c", query], universal_newlines=True)


def digest(path):
    h = hashlib.sha256()
    with open(str(path), "rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def safe_directory(path, uid=None, gid=None):
    path = pathlib.Path(path)
    if path.parent != path:
        safe_directory(path.parent)
    if path.is_symlink():
        raise RuntimeError("symlink directory rejected: " + str(path))
    if not path.exists():
        safe_directory(path.parent)
        path.mkdir(mode=0o700)
    if not path.is_dir():
        raise RuntimeError("not a directory: " + str(path))
    if uid is not None:
        os.chown(str(path), uid, gid)
        os.chmod(str(path), 0o700)


def copy_verified(source, dest, uid, gid):
    safe_directory(dest.parent, uid, gid)
    expected = digest(source)
    if dest.exists() or dest.is_symlink():
        if dest.is_symlink() or not dest.is_file() or digest(dest) != expected:
            raise RuntimeError("destination differs; no overwrite: " + str(dest))
    else:
        with open(str(source), "rb") as src, open(str(dest), "xb") as target:
            for chunk in iter(lambda: src.read(1024 * 1024), b""):
                target.write(chunk)
            target.flush()
            os.fsync(target.fileno())
    os.chown(str(dest), uid, gid)
    os.chmod(str(dest), 0o600)
    return {"source": str(source), "destination": str(dest), "sha256": expected, "bytes": source.stat().st_size}


def regular_files(root):
    for base, dirs, files in os.walk(str(root), followlinks=False):
        for name in dirs + files:
            path = pathlib.Path(base) / name
            if path.is_symlink():
                raise RuntimeError("source symlink rejected: " + str(path))
        for name in files:
            path = pathlib.Path(base) / name
            if not stat.S_ISREG(path.stat().st_mode):
                raise RuntimeError("non-regular source rejected: " + str(path))
            yield path


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--runtime", default="/storage-md0/renyuan/.gcs-webnora-runtime")
    parser.add_argument("--user-root", default="/storage-root-jfs")
    parser.add_argument("--service-root", default="/storage-root-jfs/harness-system/gcs-webnora")
    parser.add_argument("--uid", type=int, default=21090)
    parser.add_argument("--gid", type=int, default=21090)
    parser.add_argument("--apply", action="store_true")
    args = parser.parse_args()
    runtime, root, service = (pathlib.Path(p).absolute() for p in (args.runtime, args.user_root, args.service_root))
    if not root.is_mount():
        raise RuntimeError("user root must be a mounted filesystem; refusing a local fallback")
    app_env = dict(v.split("=", 1) for v in inspect("gcs-webnora-app-dev")["Config"]["Env"] if "=" in v)
    provider = app_env.get("PLATFORM_SSO_BASE_URL", "").rstrip("/")
    if not provider or "'" in provider:
        raise RuntimeError("invalid platform identity provider")
    rows = sql("SELECT row_to_json(x) FROM (SELECT t.id, array_agg(DISTINCT i.subject) AS subjects FROM tenants t JOIN tenant_members m ON m.tenant_id=t.id AND m.role='owner' AND m.status='active' AND m.deleted_at IS NULL JOIN platform_sso_identities i ON i.user_id=m.user_id AND i.provider='" + provider + "' WHERE t.deleted_at IS NULL GROUP BY t.id ORDER BY t.id) x")
    owners = {}
    for line in rows.splitlines():
        row = json.loads(line)
        subjects = row["subjects"]
        if len(subjects) != 1 or not subjects[0].isdigit() or str(int(subjects[0])) != subjects[0] or int(subjects[0]) == 0:
            raise RuntimeError("no unique numeric platform owner for workspace " + str(row["id"]))
        owners[str(row["id"])] = subjects[0]
    if sql("SELECT to_regclass('platform_storage_bindings') IS NOT NULL").strip() == "t":
        for line in sql("SELECT row_to_json(b) FROM platform_storage_bindings b").splitlines():
            binding = json.loads(line)
            if binding["provider"] != provider:
                raise RuntimeError("storage identity provider mismatch")
            if not binding["subject"].isdigit() or str(int(binding["subject"])) != binding["subject"] or int(binding["subject"]) == 0:
                raise RuntimeError("invalid stored platform user ID")
            owners[str(binding["tenant_id"])] = binding["subject"]
    plan = []
    for source in regular_files(runtime / "data/files"):
        relative = source.relative_to(runtime / "data/files")
        tenant = relative.parts[0]
        if tenant not in owners or len(relative.parts) < 2:
            raise RuntimeError("unmapped source; do not guess ownership: " + str(source))
        dest = root / ("user-" + owners[tenant]) / "gcs-webnora/spaces" / tenant / "files" / pathlib.Path(*relative.parts[1:])
        plan.append((source, dest))
    summary = {"layout": "platform-user", "owners": owners, "files": len(plan), "bytes": sum(s.stat().st_size for s, _ in plan), "database": "unchanged", "sourceRetained": True}
    if not args.apply:
        print(json.dumps(summary, ensure_ascii=False, indent=2))
        return
    for name in ("gcs-webnora-app-dev", "gcs-webnora-redis-dev"):
        if inspect(name)["State"]["Running"]:
            raise RuntimeError("stop writer before migration: " + name)
    if sql("SELECT to_regclass('platform_storage_bindings') IS NOT NULL").strip() != "t":
        raise RuntimeError("apply PostgreSQL migration 138 first")
    safe_directory(service)
    backup = service / "backups" / ("storage-migration-" + time.strftime("%Y%m%d-%H%M%S"))
    safe_directory(backup)
    env = dict(v.split("=", 1) for v in inspect("gcs-webnora-postgres-dev")["Config"]["Env"] if "=" in v)
    with open(str(backup / "postgres.dump"), "xb") as stream:
        subprocess.check_call(["docker", "exec", "gcs-webnora-postgres-dev", "pg_dump", "-U", env["POSTGRES_USER"], "-d", env["POSTGRES_DB"], "-Fc"], stdout=stream)
    os.chmod(str(backup / "postgres.dump"), 0o600)
    # Existing user-root permissions/ownership must not be rewritten. Only the
    # managed gcs-webnora subtree belongs to the restricted service account.
    for tenant, uid in owners.items():
        user = root / ("user-" + uid)
        if not user.exists():
            user.mkdir(mode=0o755)
        if user.is_symlink():
            raise RuntimeError("user root is a symlink")
        for path in (user / "gcs-webnora", user / "gcs-webnora/spaces", user / "gcs-webnora/spaces" / tenant, user / "gcs-webnora/spaces" / tenant / "files"):
            safe_directory(path, args.uid, args.gid)
        sql("INSERT INTO platform_storage_bindings(tenant_id,provider,subject) VALUES (" + tenant + ",'" + provider + "','" + uid + "') ON CONFLICT(tenant_id) DO NOTHING")
    summary["objects"] = [copy_verified(source, dest, args.uid, args.gid) for source, dest in plan]
    summary["redis"] = []
    for source in regular_files(runtime / "data/redis"):
        relative = source.relative_to(runtime / "data/redis")
        st = source.stat()
        copy_verified(source, backup / "redis" / relative, 0, 0)
        summary["redis"].append(copy_verified(source, service / "redis" / relative, st.st_uid, st.st_gid))
    st = (runtime / "data/redis").stat()
    safe_directory(service / "redis", st.st_uid, st.st_gid)
    summary["backup"] = str(backup)
    with open(str(backup / "manifest.json"), "x") as stream:
        json.dump(summary, stream, ensure_ascii=False, indent=2)
    summary.pop("objects")
    summary.pop("redis")
    print(json.dumps(summary, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    os.umask(0o077)
    main()
