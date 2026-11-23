"""Pure recorded-day reference; inputs must come from verified own read/sent."""

from datetime import datetime, timezone
from pathlib import PurePosixPath
import re


def recorded_instant(value):
    if not isinstance(value, str):
        raise ValueError("missing recorded created_at")
    match = re.fullmatch(r"(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d{1,9}))?(Z|[+-]\d{2}:\d{2})", value)
    if not match:
        raise ValueError("invalid RFC3339 created_at: " + value)
    zone = match[3]
    if zone != "Z" and (int(zone[1:3]) > 23 or int(zone[4:]) > 59):
        raise ValueError("invalid numeric timezone")
    utc = datetime.fromisoformat(match[1] + ("+00:00" if zone == "Z" else zone)).astimezone(timezone.utc)
    delta = utc - datetime(1970, 1, 1, tzinfo=timezone.utc)
    seconds = delta.days * 86400 + delta.seconds
    return seconds * 1_000_000_000 + int((match[2] or "").ljust(9, "0")), utc.date().isoformat()


def id_creation_instant(identifier):
    if not isinstance(identifier, str):
        raise ValueError("invalid message ID")
    if re.fullmatch(r"m-[0-9a-f]{32}", identifier):
        return None
    match = re.fullmatch(r"m-(\d{4})(\d{2})(\d{2})T(\d{2})(\d{2})(\d{2})\.(\d{9})Z-(?:[0-9a-f]{32}|[A-Za-z0-9]{6})", identifier)
    if not match:
        raise ValueError("invalid message ID")
    year, month, day, hour, minute, second, fraction = match.groups()
    return recorded_instant(f"{year}-{month}-{day}T{hour}:{minute}:{second}.{fraction}Z")[0]


def latest_day(records, verified_agent_directory):
    agent = PurePosixPath(verified_agent_directory)
    if not agent.is_absolute() or ".." in agent.parts or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9-]*", agent.name):
        raise ValueError("require the already verified absolute own agent directory")
    indexed, seen, copies = [], set(), {}
    for record in records:
        box, identifier = record.get("box"), record.get("id")
        if box not in {"read", "sent"}:
            raise ValueError("only valid own read/sent message records are eligible")
        id_instant = id_creation_instant(identifier)
        path = PurePosixPath(record["path"])
        if path != agent / box / (identifier + ".md") or ".." in path.parts:
            raise ValueError("metadata escaped the verified own history directory")
        if record.get("to" if box == "read" else "from") != agent.name:
            raise ValueError("message does not belong to this node's selected direction")
        if str(path) in seen:
            raise ValueError("duplicate archive path")
        seen.add(str(path))
        fingerprint = record.get("archive_sha256")
        if fingerprint is not None:
            if not isinstance(fingerprint, str) or not re.fullmatch(r"[0-9a-f]{64}", fingerprint):
                raise ValueError("invalid complete-archive digest")
            if identifier in copies and copies[identifier] != fingerprint:
                raise ValueError("same message ID has conflicting archive copies")
            copies[identifier] = fingerprint
        instant, day = recorded_instant(record.get("created_at"))
        if id_instant is not None and id_instant != instant:
            raise ValueError("message ID timestamp conflicts with recorded created_at")
        indexed.append((instant, identifier, 0 if box == "read" else 1, str(path), day, record))
    indexed.sort(key=lambda row: row[:4])
    selected_day = indexed[-1][4] if indexed else None
    selected = [row[5] for row in indexed if row[4] == selected_day]
    return {"date": selected_day, "read_count": sum(row["box"] == "read" for row in selected),
            "sent_count": sum(row["box"] == "sent" for row in selected), "messages": selected}
