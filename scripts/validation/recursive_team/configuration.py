"""Private recursive-fixture configuration assembly; not a runtime command."""

import re


def role_sections(text):
    """Read authored Markdown sections for config validation and fixture checks."""
    if len(text.encode()) > 256 * 1024:
        raise ValueError("role configuration exceeds 256 KiB")
    sections, current, fence = {}, None, None
    for line in text.splitlines(keepends=True):
        marker = re.match(r"^\s*(`{3,}|~{3,})", line)
        if marker:
            value = marker[1]
            if fence is None:
                fence = value
            elif value[0] == fence[0] and len(value) >= len(fence):
                fence = None
        match = None if fence else re.fullmatch(
            r"##\s+(?:\d+(?:\.\d+)*\.?\s+)?`([a-zA-Z0-9_-]+)`\s*", line.strip()
        )
        if match:
            current = match[1]
            if current in sections:
                raise ValueError("duplicate role/configuration section")
            sections[current] = []
        elif current is not None:
            sections[current].append(line)
    return {name: "".join(lines).strip() for name, lines in sections.items()}


def policy_text(text):
    """Compare complete policy text independently of ATX heading depth.

    The renderer's hierarchy is tested in Go. Fenced bytes and all instruction
    text must still match here; only heading markers outside fences differ.
    """
    lines, fence = [], None
    for line in text.splitlines(keepends=True):
        marker = re.match(r"^ {0,3}(`{3,}|~{3,})", line)
        if marker:
            value = marker[1]
            if fence is None:
                fence = value
            elif value[0] == fence[0] and len(value) >= len(fence) and not line[marker.end():].strip():
                fence = None
        elif fence is None:
            line = re.sub(r"^( {0,3})#{1,6}[ \t]+", r"\1", line)
        lines.append(line)
    return "".join(lines)


def compile_config(template, tree):
    sections = role_sections(template)
    if not sections.get("common_template") or not sections.get("messenger"):
        raise ValueError("template lacks common_template or messenger instructions")
    if not isinstance(tree, dict) or set(tree) != {"sessions"}:
        raise ValueError("tree must contain only a sessions list")
    sessions = tree["sessions"]
    if not isinstance(sessions, list) or not sessions:
        raise ValueError("sessions must be nonempty")
    parents = {}
    for entry in sessions:
        if not isinstance(entry, dict) or set(entry) - {"name", "parent"}:
            raise ValueError("each session accepts name and optional parent")
        name, parent = entry.get("name"), entry.get("parent")
        if not isinstance(name, str) or not re.fullmatch(r"[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}", name):
            raise ValueError("session names must be 1-128 letters/digits/dash/underscore")
        if name in parents or (parent is not None and not isinstance(parent, str)):
            raise ValueError("duplicate session or invalid parent")
        parents[name] = parent
    roots = [name for name, parent in parents.items() if parent is None]
    if len(roots) != 1:
        raise ValueError("one team config needs exactly one root")
    for name, parent in parents.items():
        if parent is not None and (parent == name or parent not in parents):
            raise ValueError("missing/self parent")
    children = {name: [] for name in parents}
    for name, parent in parents.items():
        if parent is not None:
            children[parent].append(name)
    todo, seen = roots[:], set()
    while todo:
        name = todo.pop()
        seen.add(name)
        todo.extend(children[name])
    if len(seen) != len(parents):
        raise ValueError("cycle/disconnected team tree")
    if not template.startswith("---\n"):
        raise ValueError("template needs YAML frontmatter")
    front, separator, body = template[4:].partition("\n---\n")
    if not separator or re.search(r"^(workspace_tree|interface_node):", front, re.MULTILINE):
        raise ValueError("template must not already declare a workspace tree/interface override")
    result = "---\n" + front + "\ninterface_node: messenger\n---\n" + body
    if len(result.encode()) > 256 * 1024:
        raise ValueError("compiled config exceeds 256 KiB")
    return result
