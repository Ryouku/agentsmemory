#!/usr/bin/env python3
"""mempalace_export — migrate a local mempalace into the agentsmemory SaaS.

This is a READ-ONLY exporter and pusher. It imports the frozen ``mempalace``
package purely to read your palace (it never writes to it), serialises every
drawer, diary entry, closet, knowledge-graph fact and explicit tunnel as
newline-delimited JSON (NDJSON), and — with ``--push`` — uploads that bundle to
your project's ``/import`` endpoint over HTTPS using your API token.

The upload is sent in bounded BATCHES (each a self-contained, length-delimited
POST the server reads in full before replying), then a final ``?recompute=1``
request rebuilds the derived graph once. This avoids depending on full-duplex
streaming, which the CDN/proxy in front of the SaaS does not support. The import
is idempotent — record ids are deterministic — so an interrupted migration is
finished simply by running it again, and re-runs never duplicate.

The server re-embeds every drawer with its own model, so the bundle carries TEXT
only (no vectors): it stays small and portable.

Two modes (combinable):

    # 1) Write a bundle you can inspect first:
    python mempalace_export.py --out palace.ndjson

    # 2) Stream straight to the SaaS:
    python mempalace_export.py --push \\
        --server https://memory.example.com --token sk_live_xxx

Pass ``--wing NAME`` (repeatable) to export only part of a palace — one project
into one workspace, rather than everything you have ever filed.

Run it where the ``mempalace`` package is importable (your existing install), or
pass ``--mempalace-path /path/to/mempalace-frozen`` to point at it.
"""

import argparse
import http.client
import json
import os
import re
import sqlite3
import sys
from urllib.parse import urlparse

# A bare YYYY-MM-DD prefix is all the SaaS accepts for KG validity bounds (its
# temporal columns are compared as text, so it allows only a calendar date or a
# canonical UTC datetime). The frozen palace stamps naive microsecond datetimes,
# which would be rejected, so we coarsen every temporal to its date — the day
# granularity that KG validity windows are reasoned about in anyway.
_DATE_PREFIX = re.compile(r"^(\d{4}-\d{2}-\d{2})")


def _date_only(value):
    """Coarsen a temporal value to YYYY-MM-DD, or "" when absent/unparseable."""
    if not value:
        return ""
    m = _DATE_PREFIX.match(str(value).strip())
    return m.group(1) if m else ""


def _as_entities(value):
    """Normalise the stored entities field (list, or ';'/',' joined str) to a list."""
    if isinstance(value, list):
        return [str(e) for e in value if str(e).strip()]
    if isinstance(value, str):
        parts = re.split(r"[;,]", value)
        return [p.strip() for p in parts if p.strip()]
    return []


def _open_collection(get_collection, palace_path, name=None):
    """Open a palace collection, retrying without the embedder-identity check.

    A pure metadata/document read does not need the embedder, but get_collection
    enforces model identity by default; the skip lets export work even when the
    recorded model differs from what is installed (or Ollama is down).
    """
    kwargs = {"create": False}
    if name:
        kwargs["collection_name"] = name
    try:
        return get_collection(palace_path, **kwargs)
    except TypeError:
        # Older signature without collection_name kw — fall back to positional.
        return get_collection(palace_path, create=False)
    except Exception:
        try:
            return get_collection(palace_path, _skip_identity_check=True, **kwargs)
        except Exception:
            return None


def _iter_collection(col, batch=1000):
    """Yield (id, document, metadata) for every record, paginated to bound memory."""
    total = col.count()
    offset = 0
    while offset < total:
        page = col.get(limit=batch, offset=offset, include=["documents", "metadatas"])
        ids = page.get("ids") or []
        if not ids:
            break
        docs = page.get("documents") or []
        metas = page.get("metadatas") or []
        for i, rec_id in enumerate(ids):
            yield rec_id, docs[i], (metas[i] or {})
        offset += len(ids)


def iter_drawers(get_collection, palace_path, wings=None):
    """Yield drawer records (diary entries included — they are just drawers with
    a room and an agent/topic). ``wings``, when given, keeps only those wings."""
    col = _open_collection(get_collection, palace_path)
    if col is None:
        print("  warning: could not open the drawer collection — skipping drawers", file=sys.stderr)
        return
    for _id, doc, meta in _iter_collection(col):
        if not doc:
            continue
        if wings is not None and meta.get("wing", "") not in wings:
            continue
        yield {
            "kind": "drawer",
            "wing": meta.get("wing", ""),
            "room": meta.get("room", "general"),
            "source_file": meta.get("source_file", ""),
            "chunk_index": int(meta.get("chunk_index", 0) or 0),
            "content": doc,
            "entities": _as_entities(meta.get("entities")),
            "filed_at": meta.get("filed_at", ""),
            "content_date": meta.get("content_date", ""),
            # The frozen palace records the author as ``added_by``; a diary entry
            # also carries ``agent``/``topic``. Prefer the explicit diary keys.
            "agent": meta.get("agent", meta.get("added_by", "")),
            "topic": meta.get("topic", ""),
        }


def iter_closets(get_closets_collection, palace_path, wings=None):
    """Yield closet pointer-index records, optionally limited to ``wings``."""
    col = _open_collection(get_closets_collection, palace_path)
    if col is None:
        return
    for _id, doc, meta in _iter_collection(col):
        if not doc:
            continue
        if wings is not None and meta.get("wing", "") not in wings:
            continue
        yield {
            "kind": "closet",
            "wing": meta.get("wing", ""),
            "room": meta.get("room", "general"),
            "source_file": meta.get("source_file", ""),
            "document": doc,
            "entities": _as_entities(meta.get("entities")),
            "filed_at": meta.get("filed_at", ""),
        }


def iter_kg(kg_db):
    """Yield knowledge-graph facts with subject/object resolved to display names.

    Reads the KG sqlite directly (read-only). subject/object are entity ids in
    the triples table; we join entities to emit human names, which the SaaS
    re-normalises on its side.
    """
    if not kg_db or not os.path.isfile(kg_db):
        return
    uri = "file:%s?mode=ro" % kg_db
    conn = sqlite3.connect(uri, uri=True)
    try:
        conn.row_factory = sqlite3.Row
        rows = conn.execute(
            """
            SELECT s.name AS subject, t.predicate AS predicate, o.name AS object,
                   t.valid_from AS valid_from, t.valid_to AS valid_to,
                   t.source_file AS source_file
            FROM triples t
            JOIN entities s ON s.id = t.subject
            JOIN entities o ON o.id = t.object
            """
        ).fetchall()
    finally:
        conn.close()
    for r in rows:
        if not r["subject"] or not r["predicate"] or not r["object"]:
            continue
        yield {
            "kind": "kg",
            "subject": r["subject"],
            "predicate": r["predicate"],
            "object": r["object"],
            "valid_from": _date_only(r["valid_from"]),
            "valid_to": _date_only(r["valid_to"]),
            "source_file": r["source_file"] or "",
        }


def iter_tunnels(list_tunnels, wings=None):
    """Yield explicit (user-authored) cross-wing tunnels. Entity/topic tunnels are
    derived state the SaaS regenerates after import, so they are not exported.

    With ``wings`` set, BOTH endpoints must be inside the selection: the importer
    defers tunnels until every drawer is in place and then validates that each
    endpoint room actually holds one, so a tunnel leaving the exported wings would
    fail there. Dropping it here is the difference between a clean import and an
    error the user has to interpret."""
    try:
        tunnels = list_tunnels()
    except Exception:
        return
    for t in tunnels or []:
        if t.get("kind", "explicit") != "explicit":
            continue
        src = t.get("source") or {}
        tgt = t.get("target") or {}
        if not src.get("wing") or not tgt.get("wing"):
            continue
        if wings is not None and (src["wing"] not in wings or tgt["wing"] not in wings):
            continue
        yield {
            "kind": "tunnel",
            "source_wing": src.get("wing", ""),
            "source_room": src.get("room", ""),
            "target_wing": tgt.get("wing", ""),
            "target_room": tgt.get("room", ""),
            "label": t.get("label", ""),
        }


def _counts(get_collection, get_closets_collection, list_tunnels, palace_path, kg_db,
            wings=None, with_kg=True, drawer_counts=None):
    """Up-front totals for the manifest, so the importer can show a real progress
    bar. Each count is best-effort and defaults to 0.

    Unfiltered, the collections answer with a cheap count(). With ``wings`` set
    that count is the wrong number, so the collections are walked and matched
    instead — one extra read-only pass, which is the price of a manifest that
    describes the bundle actually being sent."""
    total = 0
    if wings is not None and drawer_counts is not None:
        # The caller already surveyed the palace (to validate --wing), so the
        # per-wing drawer counts are known: reuse them rather than walking a
        # 30k-drawer collection a second time for the same answer.
        total += sum(drawer_counts.get(w, 0) for w in wings)
    else:
        try:
            col = _open_collection(get_collection, palace_path)
            if col is not None:
                total += col.count() if wings is None else _count_matching(col, wings)
        except Exception:
            pass
    try:
        cc = _open_collection(get_closets_collection, palace_path)
        if cc is not None:
            total += cc.count() if wings is None else _count_matching(cc, wings)
    except Exception:
        pass
    if with_kg and kg_db and os.path.isfile(kg_db):
        try:
            conn = sqlite3.connect("file:%s?mode=ro" % kg_db, uri=True)
            total += conn.execute("SELECT COUNT(*) FROM triples").fetchone()[0]
            conn.close()
        except Exception:
            pass
    try:
        total += sum(1 for _ in iter_tunnels(list_tunnels, wings))
    except Exception:
        pass
    return total


def _count_matching(col, wings):
    """Count the records in a collection whose wing is in ``wings``."""
    return sum(1 for _id, doc, meta in _iter_collection(col) if doc and meta.get("wing", "") in wings)


def survey_wings(get_collection, palace_path):
    """Return {wing: drawer count} for every wing that holds drawers.

    Serves two jobs: --list-wings prints it, and --wing is checked against it, so
    a mistyped name fails here instead of exporting a bundle of nothing that looks
    like a successful migration."""
    col = _open_collection(get_collection, palace_path)
    counts = {}
    if col is None:
        return counts
    for _id, doc, meta in _iter_collection(col):
        if not doc:
            continue
        wing = meta.get("wing", "")
        counts[wing] = counts.get(wing, 0) + 1
    return counts


def records(mp, palace_path, kg_db, wings=None, with_kg=True, drawer_counts=None):
    """The full export stream: a manifest line, then every record kind in the
    order the importer needs (drawers first so tunnel endpoints exist).

    ``wings`` limits drawers, closets and tunnels to a selection. Knowledge-graph
    facts carry no wing — they are palace-global triples — so they are governed by
    ``with_kg`` instead: excluded by default alongside a wing filter, because
    "export this one project" should not sweep every other project's facts into
    that workspace."""
    total = _counts(mp.get_collection, mp.get_closets_collection, mp.list_tunnels,
                    palace_path, kg_db, wings, with_kg, drawer_counts)
    yield {"kind": "manifest", "total": total, "source": "mempalace"}
    yield from iter_drawers(mp.get_collection, palace_path, wings)
    yield from iter_closets(mp.get_closets_collection, palace_path, wings)
    if with_kg:
        yield from iter_kg(kg_db)
    yield from iter_tunnels(mp.list_tunnels, wings)


class _Mempalace:
    """Thin holder for the frozen package's read functions, so the rest of the
    module does not care where they were imported from."""

    def __init__(self):
        from mempalace.palace import get_collection, get_closets_collection
        from mempalace.palace_graph import list_tunnels

        self.get_collection = get_collection
        self.get_closets_collection = get_closets_collection
        self.list_tunnels = list_tunnels


# --------------------------------------------------------------------------
# Local source of truth
#
# A palace keeps its durable copy of every drawer in source_of_truth.sqlite —
# wing, room, document text and metadata, with the embedding vector in a column
# we do not read. The vector store (chroma, or a remote qdrant) is an INDEX over
# that table, so exporting through the mempalace package means talking to a
# service we have no use for: an export needs text, never vectors, and a remote
# qdrant turns `--list-wings` into a network round trip per page.
#
# Reading the sqlite directly makes the export local, offline and fast — a wing
# survey becomes one indexed GROUP BY (idx_drawers_coll_wing_room) instead of a
# full walk of the collection.
# --------------------------------------------------------------------------

# Collection names are prefixed per palace (mempalace_<hash>_mempalace_drawers),
# so the tables are matched by suffix rather than by full name.
DRAWER_COLLECTION_SUFFIX = "mempalace_drawers"
CLOSET_COLLECTION_SUFFIX = "mempalace_closets"


def _locate_palace_file(name, palace_path):
    """Find a palace-level file, searching the layouts mempalace actually uses.

    ``palace_path`` points at the vector-store directory (e.g. ~/.mempalace/palace),
    but the durable sqlite, tunnels.json and config.json sit one level up in the
    mempalace home. Checking the parent — and the conventional ~/.mempalace last —
    is what keeps this working across both layouts instead of silently falling back
    to the slow path when the file was there all along."""
    seen = []
    for base in (palace_path, os.path.dirname(palace_path.rstrip(os.sep)),
                 os.path.expanduser("~/.mempalace")):
        if not base or base in seen:
            continue
        seen.append(base)
        candidate = os.path.join(base, name)
        if os.path.isfile(candidate):
            return candidate
    return os.path.join(palace_path, name)  # non-existent: callers report it


def _sqlite_ro(db):
    """Open a database read-only, so an export can never modify a live palace."""
    return sqlite3.connect("file:%s?mode=ro" % db, uri=True)


def _sqlite_records(db, suffix, wings):
    """Yield (wing, room, document, metadata) for one collection, wings-filtered.

    The wing filter runs in SQL rather than in Python: the table is indexed on
    (collection, wing, room), so a single-wing export of a 33k-drawer palace reads
    only the rows it will emit."""
    sql = ("SELECT wing, room, document, metadata_json FROM drawers "
           "WHERE collection LIKE ?")
    params = ["%" + suffix]
    if wings:
        sql += " AND wing IN (%s)" % ",".join("?" for _ in wings)
        params.extend(sorted(wings))
    conn = _sqlite_ro(db)
    try:
        for wing, room, document, meta_json in conn.execute(sql, params):
            if not document:
                continue
            try:
                meta = json.loads(meta_json) if meta_json else {}
            except ValueError:
                meta = {}
            yield wing or meta.get("wing", ""), room or meta.get("room", "general"), document, meta
    finally:
        conn.close()


def iter_drawers_sqlite(db, wings=None):
    """Yield drawer records straight from the local sqlite."""
    for wing, room, document, meta in _sqlite_records(db, DRAWER_COLLECTION_SUFFIX, wings):
        yield {
            "kind": "drawer",
            "wing": wing,
            "room": room,
            "source_file": meta.get("source_file", ""),
            "chunk_index": int(meta.get("chunk_index", 0) or 0),
            "content": document,
            "entities": _as_entities(meta.get("entities")),
            "filed_at": meta.get("filed_at", ""),
            "content_date": meta.get("content_date", ""),
            "agent": meta.get("agent", meta.get("added_by", "")),
            "topic": meta.get("topic", ""),
        }


def iter_closets_sqlite(db, wings=None):
    """Yield closet records straight from the local sqlite."""
    for wing, room, document, meta in _sqlite_records(db, CLOSET_COLLECTION_SUFFIX, wings):
        yield {
            "kind": "closet",
            "wing": wing,
            "room": room,
            "source_file": meta.get("source_file", ""),
            "document": document,
            "entities": _as_entities(meta.get("entities")),
            "filed_at": meta.get("filed_at", ""),
        }


def survey_wings_sqlite(db):
    """Return {wing: drawer count} with one indexed GROUP BY."""
    conn = _sqlite_ro(db)
    try:
        rows = conn.execute(
            "SELECT wing, COUNT(*) FROM drawers WHERE collection LIKE ? GROUP BY wing",
            ["%" + DRAWER_COLLECTION_SUFFIX],
        ).fetchall()
    finally:
        conn.close()
    return {wing or "": n for wing, n in rows}


def count_sqlite(db, suffix, wings):
    """Count the rows one collection would contribute to the bundle."""
    sql = "SELECT COUNT(*) FROM drawers WHERE collection LIKE ?"
    params = ["%" + suffix]
    if wings:
        sql += " AND wing IN (%s)" % ",".join("?" for _ in wings)
        params.extend(sorted(wings))
    conn = _sqlite_ro(db)
    try:
        return conn.execute(sql, params).fetchone()[0]
    finally:
        conn.close()


def load_tunnels_json(path):
    """Read explicit tunnels from the palace's tunnels.json.

    Returns a callable so it plugs into iter_tunnels unchanged — the JSON records
    already carry the kind/source/target shape the package's list_tunnels()
    returns."""
    def _list():
        try:
            with open(path, "r", encoding="utf-8") as fh:
                data = json.load(fh)
        except Exception:
            return []
        return data if isinstance(data, list) else []
    return _list


def records_sqlite(db, kg_db, tunnels_path, wings=None, with_kg=True):
    """The export stream, read entirely from local files.

    Same records, same order, same manifest contract as records(); only the
    source differs."""
    total = count_sqlite(db, DRAWER_COLLECTION_SUFFIX, wings)
    total += count_sqlite(db, CLOSET_COLLECTION_SUFFIX, wings)
    list_tunnels = load_tunnels_json(tunnels_path)
    total += sum(1 for _ in iter_tunnels(list_tunnels, wings))
    if with_kg and kg_db and os.path.isfile(kg_db):
        try:
            conn = _sqlite_ro(kg_db)
            total += conn.execute("SELECT COUNT(*) FROM triples").fetchone()[0]
            conn.close()
        except Exception:
            pass

    yield {"kind": "manifest", "total": total, "source": "mempalace"}
    yield from iter_drawers_sqlite(db, wings)
    yield from iter_closets_sqlite(db, wings)
    if with_kg:
        yield from iter_kg(kg_db)
    yield from iter_tunnels(list_tunnels, wings)


def _default_palace_path():
    """The configured palace path, or the conventional ~/.mempalace fallback."""
    try:
        from mempalace.config import MempalaceConfig

        return os.path.abspath(os.path.expanduser(MempalaceConfig().palace_path))
    except Exception:
        return os.path.expanduser("~/.mempalace")


def _default_kg_db():
    try:
        from mempalace.knowledge_graph import DEFAULT_KG_PATH

        return DEFAULT_KG_PATH
    except Exception:
        return os.path.expanduser("~/.mempalace/knowledge_graph.sqlite3")


def write_file(stream, path):
    """Write the NDJSON bundle to a file, returning how many records were written."""
    n = 0
    with open(path, "w", encoding="utf-8") as f:
        for rec in stream:
            f.write(json.dumps(rec, ensure_ascii=False))
            f.write("\n")
            n += 1
    return n


def _byte_lines(path_or_stream):
    """Yield NDJSON byte lines from a file path or an in-memory record stream."""
    if isinstance(path_or_stream, str):
        with open(path_or_stream, "rb") as f:
            for line in f:
                yield line
    else:
        for rec in path_or_stream:
            yield (json.dumps(rec, ensure_ascii=False) + "\n").encode("utf-8")


# Records per /import POST. The server reads each request in full and re-embeds
# every record before it replies (no streaming), so a batch must finish well under
# a CDN/reverse-proxy read timeout (~100s in front of the SaaS). Embedding
# throughput — not bytes — is the limit, so this is sized in records: 500 keeps a
# batch to a few seconds on a typical embedder. Tune with --batch.
DEFAULT_BATCH = 500


def _open(u):
    """Open an HTTP(S) connection for a parsed URL."""
    if u.scheme == "https":
        return http.client.HTTPSConnection(u.hostname, u.port or 443)
    return http.client.HTTPConnection(u.hostname, u.port or 80)


def _send(conn, path, body, headers):
    """POST and return the response, surfacing an early server reply (e.g. the 401
    the gate writes before it reads the body) instead of an opaque BrokenPipeError
    when the server closes the socket mid-send. The response bytes are usually
    already buffered, so a second getresponse() recovers the real status."""
    try:
        conn.request("POST", path, body=body, headers=headers)
        return conn.getresponse()
    except (BrokenPipeError, ConnectionResetError):
        try:
            return conn.getresponse()
        except Exception:
            raise SystemExit(
                "  push failed: the server closed the connection during upload.\n"
                "  Usually a bad/expired --token, or a batch too large for a proxy\n"
                "  limit — lower --batch and retry (the import is idempotent)."
            )


def _post(u, token, body_bytes, recompute=False):
    """Send one batch (or the finalize) as a length-delimited POST and return the
    server's JSON summary. Length-delimited (Content-Length), NEVER chunked: a CDN
    such as Cloudflare answers a chunked request body with a 520 and breaks the
    socket. Each batch is small, so the body is held in memory as bytes — no
    chunked generator, no temp file."""
    conn = _open(u)
    path = u.path + ("?recompute=1" if recompute else "")
    headers = {
        "Authorization": "Bearer " + token,
        "Content-Type": "application/x-ndjson",
        "Content-Length": str(len(body_bytes)),
    }
    try:
        resp = _send(conn, path, body_bytes, headers)
        data = resp.read()
        if resp.status >= 400:
            raise SystemExit("  push failed: HTTP %d — %s" % (
                resp.status, data.decode("utf-8", "replace").strip()))
        # The server replies with a single JSON summary object. A 2xx with an
        # empty or unparseable body is NOT success — the batch's fate is unknown,
        # so fail loudly rather than silently dropping the pending records.
        text = data.decode("utf-8", "replace").strip()
        try:
            summary = json.loads(text.splitlines()[-1]) if text else None
        except (ValueError, IndexError):
            summary = None
        if not isinstance(summary, dict):
            raise SystemExit(
                "  push failed: unexpected server response (HTTP %d): %s"
                % (resp.status, text[:200] or "<empty>"))
        return summary
    finally:
        conn.close()


def push(source, server, token, batch=DEFAULT_BATCH):
    """Migrate the NDJSON bundle to <server>/import in bounded batches, then
    finalize. Each batch is a complete length-delimited POST the server reads in
    full before replying, so nothing depends on full-duplex streaming (which a
    CDN/proxy in front of the SaaS does not support, and which silently truncated
    the old chunked upload). A closing ``?recompute=1`` request rebuilds the
    derived graph once. Re-running is safe: every record id is deterministic, so
    batches upsert rather than duplicate, and an interrupted migration is finished
    simply by running it again."""
    u = urlparse(server.rstrip("/") + "/import")
    acc = {"drawers": 0, "closets": 0, "kg_facts": 0, "tunnels": 0, "skipped": 0}
    total = 0
    pending = []

    def flush():
        if not pending:
            return
        summary = _post(u, token, b"".join(pending))
        # The server returns 200 + an "error" field for a mid-batch decode error
        # or a body that hit its size cap: some records in this batch were NOT
        # filed. Treat that as a failure (the refactor exists to stop silent
        # partial imports), not success. The import is idempotent, so the user
        # fixes the cause and re-runs to finish.
        if summary.get("error"):
            raise SystemExit(
                "\n  push failed mid-batch: %s\n"
                "  Some records may already be filed; the import is idempotent, so\n"
                "  fix the cause (e.g. lower --batch) and re-run to finish." % summary["error"])
        for k in acc:
            acc[k] += int(summary.get(k, 0) or 0)
        del pending[:]
        _print_running(acc, total)

    for i, line in enumerate(_byte_lines(source)):
        if i == 0:
            # The first line may be the manifest: capture its grand total for the
            # progress denominator and don't send it (it is not a stored record).
            try:
                rec0 = json.loads(line.decode("utf-8", "replace"))
            except ValueError:
                rec0 = {}
            if rec0.get("kind") == "manifest":
                total = int(rec0.get("total", 0) or 0)
                continue
        pending.append(line)
        if len(pending) >= batch:
            flush()
    flush()

    # Finalize: one request rebuilds the derived graph (hallways + entity/topic
    # tunnels) for the whole tenant, after every batch is filed. Idempotent, so a
    # timed-out finalize can just be re-run.
    summary = _post(u, token, b"", recompute=True)
    _print_done(acc, summary, total)


def _print_running(acc, total):
    """Overwrite a single running progress line as batches land."""
    filed = acc["drawers"] + acc["closets"] + acc["kg_facts"] + acc["tunnels"]
    tail = "/%d" % total if total else ""
    sys.stdout.write(
        "\r  filed %d%s  (drawers %d, closets %d, facts %d, tunnels %d, skipped %d)" % (
            filed, tail, acc["drawers"], acc["closets"], acc["kg_facts"],
            acc["tunnels"], acc["skipped"]))
    sys.stdout.flush()


def _print_done(acc, final_summary, total):
    """Print the final summary after the finalize request returns."""
    filed = acc["drawers"] + acc["closets"] + acc["kg_facts"] + acc["tunnels"]
    tail = "/%d" % total if total else ""
    print(
        "\n  done.  filed %d%s  (drawers %d, closets %d, facts %d, tunnels %d, skipped %d)"
        "  hallways rebuilt: %d" % (
            filed, tail, acc["drawers"], acc["closets"], acc["kg_facts"],
            acc["tunnels"], acc["skipped"], int(final_summary.get("hallways", 0) or 0)))
    if final_summary.get("error"):
        print("  finalize error: %s" % final_summary["error"])


def main(argv=None):
    ap = argparse.ArgumentParser(
        description="Export a local mempalace and migrate it into the agentsmemory SaaS.",
    )
    ap.add_argument("--palace", default=None, help="Palace directory (default: your configured palace).")
    ap.add_argument("--source-db", default=None,
                    help="Palace source_of_truth.sqlite (default: <palace>/source_of_truth.sqlite). "
                         "Read directly, so the export needs no vector store and no network.")
    ap.add_argument("--via-package", action="store_true",
                    help="Read through the mempalace package instead of the local sqlite "
                         "(slower, and hits a remote vector store if the palace uses one).")
    ap.add_argument("--kg-db", default=None, help="Knowledge-graph sqlite path (default: ~/.mempalace/knowledge_graph.sqlite3).")
    ap.add_argument("--mempalace-path", default=None, help="Path to the mempalace package if it is not already importable.")
    ap.add_argument("--file", default=None, help="Push a previously exported NDJSON bundle (skips reading a palace).")
    ap.add_argument("--out", default=None, help="Write the NDJSON bundle to this file.")
    ap.add_argument("--push", action="store_true", help="Stream the bundle to the SaaS /import endpoint.")
    ap.add_argument("--server", default=None, help="SaaS base URL, e.g. https://memory.example.com (with --push).")
    ap.add_argument("--token", default=os.environ.get("AGENTSMEMORY_TOKEN"), help="Project API token / Bearer (or set AGENTSMEMORY_TOKEN).")
    ap.add_argument("--batch", type=int, default=DEFAULT_BATCH,
                    help="Records per /import request (default %d). Lower it if a batch times out behind a proxy." % DEFAULT_BATCH)
    ap.add_argument("--wing", action="append", default=None, metavar="NAME",
                    help="Export only this wing (repeatable). Default: the whole palace.")
    ap.add_argument("--with-kg", action="store_true",
                    help="With --wing, also export knowledge-graph facts. They carry no wing, "
                         "so this exports the WHOLE graph — every project's facts, not just this wing's.")
    ap.add_argument("--list-wings", action="store_true",
                    help="Print the wings that hold drawers, with their record counts, and exit.")
    args = ap.parse_args(argv)
    if args.batch < 1:
        ap.error("--batch must be >= 1")

    if args.with_kg and not args.wing:
        ap.error("--with-kg only means something with --wing; a full export already includes the graph")
    if not args.out and not args.push and not args.list_wings:
        ap.error("nothing to do: pass --out FILE and/or --push (or --list-wings)")
    if args.push and (not args.server or not args.token):
        ap.error("--push requires --server and --token (or AGENTSMEMORY_TOKEN)")

    # Pushing a pre-exported bundle needs no palace and no mempalace package — it
    # is a pure upload, usable from any machine that holds the file.
    if args.file:
        if not args.push:
            ap.error("--file is a bundle to push; pass --push with --server and --token")
        print("  pushing %s to %s/import ..." % (args.file, args.server.rstrip("/")))
        push(args.file, args.server, args.token, args.batch)
        return

    if args.mempalace_path:
        sys.path.insert(0, os.path.abspath(os.path.expanduser(args.mempalace_path)))

    palace_path = os.path.abspath(os.path.expanduser(args.palace)) if args.palace else _default_palace_path()
    kg_db = os.path.abspath(os.path.expanduser(args.kg_db)) if args.kg_db else _default_kg_db()
    source_db = (os.path.abspath(os.path.expanduser(args.source_db)) if args.source_db
                 else _locate_palace_file("source_of_truth.sqlite", palace_path))

    # Prefer the palace's own sqlite: it holds every drawer's text and metadata,
    # needs no mempalace install, and — unlike the collection API — never reaches
    # for a vector store the export has no use for.
    use_sqlite = not args.via_package and os.path.isfile(source_db)
    mp = None
    if not use_sqlite:
        try:
            mp = _Mempalace()
        except Exception as exc:
            raise SystemExit(
                "  no source_of_truth.sqlite at %s, and the mempalace package is not importable (%s).\n"
                "  Pass --source-db, or run this where mempalace is installed (--mempalace-path)." % (source_db, exc)
            )

    tunnels_path = _locate_palace_file("tunnels.json", palace_path)
    print("  palace: %s" % palace_path)
    print("  source: %s" % (source_db if use_sqlite else "mempalace package (vector store)"))
    print("  kg db:  %s" % (kg_db if os.path.isfile(kg_db) else "(none)"))

    if args.list_wings:
        counts = survey_wings_sqlite(source_db) if use_sqlite else survey_wings(mp.get_collection, palace_path)
        for wing in sorted(counts, key=lambda w: (-counts[w], w)):
            print("  %-32s %d drawers" % (wing or "(no wing)", counts[wing]))
        return

    wings = None
    with_kg = True
    drawer_counts = None
    if args.wing:
        wings = set(args.wing)
        # Reject a mistyped wing here rather than shipping an empty bundle that
        # looks like a successful migration.
        if use_sqlite:
            drawer_counts = survey_wings_sqlite(source_db)
        else:
            print("  surveying wings ...")
            drawer_counts = survey_wings(mp.get_collection, palace_path)
        known = drawer_counts
        missing = sorted(w for w in wings if w not in known)
        if missing:
            raise SystemExit(
                "  no such wing: %s\n  available: %s"
                % (", ".join(missing), ", ".join(sorted(w for w in known if w)) or "(none)")
            )
        with_kg = bool(args.with_kg)
        print("  wings:  %s" % ", ".join(sorted(wings)))
        print("  kg:     %s" % (
            "included (the WHOLE graph — facts carry no wing)" if with_kg
            else "excluded (facts carry no wing; pass --with-kg to include them all)"))

    # Write a file first when asked, then push from it (re-reading the palace
    # twice is wasteful and non-atomic for a large export).
    def stream():
        if use_sqlite:
            return records_sqlite(source_db, kg_db, tunnels_path, wings, with_kg)
        return records(mp, palace_path, kg_db, wings, with_kg, drawer_counts)

    if args.out:
        n = write_file(stream(), args.out)
        print("  wrote %d records -> %s" % (n, args.out))

    if args.push:
        source = args.out if args.out else stream()
        print("  pushing to %s/import ..." % args.server.rstrip("/"))
        push(source, args.server, args.token, args.batch)


if __name__ == "__main__":
    main()
