#!/usr/bin/env python3
"""Generate content-addressed guides; --check validates without writes.

Canonical hash input is UTF-8 sorted-key compact JSON (ensure_ascii=False)
containing declarations plus SHA-256 of each source's bytes with generated
frontmatter guideSetId/manifestHash removed, and each served markdown body.
Generated identities are excluded to avoid self-reference. ManifestHash and
GuideSetID use the same SHA-256 digest. Changing source text or declarations
changes the identity; a running binary serves only its embedded immutable set.
"""

import hashlib
import json
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]
PACKAGES = ROOT / "skills"
EMBEDDED = ROOT / "internal/guide"


def digest(data):
    return "sha256:" + hashlib.sha256(data).hexdigest()


def canonical(data):
    return json.dumps(
        data, sort_keys=True, separators=(",", ":"), ensure_ascii=False
    ).encode()


def package_path(package: str, relative: str) -> pathlib.Path:
    """Resolve a declared package path without allowing lexical or symlink escapes."""
    if not isinstance(package, str) or not re.fullmatch(
        r"[a-z0-9]+(?:-[a-z0-9]+)*", package
    ):
        raise SystemExit(f"invalid package: {package!r}")
    if (
        not isinstance(relative, str)
        or not relative
        or relative.startswith("/")
        or "\\" in relative
        or any(part in ("", ".", "..") for part in relative.split("/"))
    ):
        raise SystemExit(f"expected nonempty relative POSIX path: {relative!r}")
    packages = PACKAGES.resolve()
    directory = (PACKAGES / package).resolve()
    path = directory.joinpath(relative).resolve()
    if not packages.is_relative_to(PACKAGES) or not directory.is_relative_to(packages):
        raise SystemExit(f"package {package!r} escapes repository skills directory")
    if not path.is_relative_to(directory):
        raise SystemExit(f"path {relative!r} escapes package {package!r}")
    return path


def embedded_destination(filename):
    directory = EMBEDDED.resolve()
    path = (EMBEDDED / filename).resolve()
    if not directory.is_relative_to(EMBEDDED) or not path.is_relative_to(directory):
        raise SystemExit(f"path {filename!r} escapes embedded directory")
    return path


def embedded_path(filename: str) -> pathlib.Path:
    if (
        not isinstance(filename, str)
        or not filename.endswith(".md")
        or filename == ".md"
        or "/" in filename
        or "\\" in filename
    ):
        raise SystemExit(f"expected plain .md filename: {filename!r}")
    return embedded_destination(filename)


def reserve(destinations, path, owner):
    """Every canonical/input/output path has one owner, including symlink aliases."""
    for existing, existing_owner in destinations.items():
        if path == existing or path in existing.parents or existing in path.parents:
            raise SystemExit(
                f"destination collision: {owner} ({path}) and "
                f"{existing_owner} ({existing}) overlap"
            )
    if path.exists() and not path.is_file():
        raise SystemExit(f"destination must be a file: {path}")
    for parent in path.parents:
        if parent.exists() and not parent.is_dir():
            raise SystemExit(f"destination parent must be a directory: {parent}")
    destinations[path] = owner


def validate_workflows(declarations, sources):
    workflows = declarations.get("workflows")
    if not isinstance(workflows, list) or not workflows:
        raise SystemExit("workflows must be a nonempty list")
    indexed = {}
    for workflow in workflows:
        if not isinstance(workflow, dict):
            raise SystemExit("workflow must be an object")
        for field in ("workflowId", "workflowVersion", "entrypoint"):
            if not isinstance(workflow.get(field), str) or not workflow[field]:
                raise SystemExit(f"workflow requires nonempty {field}")
        identity = workflow["workflowId"]
        if identity in indexed:
            raise SystemExit(f"duplicate workflow: {identity}")
        for field in ("requiredModelSchemaVersions", "requiredCapabilities"):
            if not isinstance(workflow.get(field), list) or any(
                not isinstance(item, str) or not item for item in workflow[field]
            ):
                raise SystemExit(f"workflow {identity}: {field} must be a string list")
        if "requiredViewSchemaVersions" in workflow and (
            not isinstance(workflow["requiredViewSchemaVersions"], list)
            or not workflow["requiredViewSchemaVersions"]
            or any(
                not isinstance(item, str) or not item
                for item in workflow["requiredViewSchemaVersions"]
            )
        ):
            raise SystemExit(
                f"workflow {identity}: requiredViewSchemaVersions must be a nonempty string list"
            )
        entrypoint = workflow["entrypoint"]
        if entrypoint not in sources:
            raise SystemExit(f"workflow {identity}: unknown entrypoint {entrypoint}")
        if sources[entrypoint]["workflowId"] != identity:
            raise SystemExit(
                f"workflow {identity}: entrypoint belongs to another workflow"
            )
        indexed[identity] = workflow
    for source in sources.values():
        if source["workflowId"] not in indexed:
            raise SystemExit(
                f"topic {source['topic']}: unknown workflow {source['workflowId']}"
            )
    return workflows


def without_identity(text):
    if not text.startswith("---\n"):
        return text
    front, separator, body = text[4:].partition("\n---\n")
    front = "\n".join(
        line
        for line in front.splitlines()
        if not line.startswith(("  guideSetId:", "  manifestHash:"))
    )
    return "---\n" + front + separator + body


def body(text):
    if text.startswith("---\n"):
        _, separator, rest = text[4:].partition("\n---\n")
        if separator:
            return rest.lstrip("\n")
    return text


def validate_metadata(text, workflow):
    """Check entrypoint metadata using the deliberately simple frontmatter schema."""
    if not text.startswith("---\n"):
        raise SystemExit(f"entrypoint {workflow['entrypoint']}: missing frontmatter")
    front, separator, _ = text[4:].partition("\n---\n")
    if not separator:
        raise SystemExit(
            f"entrypoint {workflow['entrypoint']}: missing frontmatter terminator"
        )
    metadata = False
    fields = {}
    for line in front.splitlines():
        if line == "metadata:":
            metadata = True
            continue
        if metadata and line.startswith("  ") and ":" in line:
            key, value = line.strip().split(":", 1)
            try:
                fields[key] = json.loads(value.strip())
            except json.JSONDecodeError:
                raise SystemExit(
                    f"entrypoint {workflow['entrypoint']}: metadata must use JSON string scalars"
                ) from None
            if not isinstance(fields[key], str):
                raise SystemExit(
                    f"entrypoint {workflow['entrypoint']}: metadata must use JSON string scalars"
                )
        elif line and not line.startswith(" "):
            metadata = False
    for name in ("workflowId", "workflowVersion"):
        if fields.get(name) != workflow[name]:
            raise SystemExit(f"entrypoint {workflow['entrypoint']}: incorrect {name}")
    for name in (
        "requiredModelSchemaVersions",
        "requiredCapabilities",
        *(
            ["requiredViewSchemaVersions"]
            if "requiredViewSchemaVersions" in workflow
            else []
        ),
    ):
        try:
            items = json.loads(fields.get(name, ""))
        except (json.JSONDecodeError, TypeError):
            raise SystemExit(
                f"entrypoint {workflow['entrypoint']}: {name} must be a JSON-list string"
            ) from None
        if items != workflow[name]:
            raise SystemExit(f"entrypoint {workflow['entrypoint']}: incorrect {name}")


def generate(check: bool) -> None:
    declaration_path = package_path("mocker", "guide-sources.json")
    declarations = json.loads(declaration_path.read_text())
    if not isinstance(declarations, dict) or not isinstance(
        declarations.get("sources"), list
    ):
        raise SystemExit("sources must be a list")

    # Preflight all ownership and containment before reading metadata or creating
    # output directories. The declaration itself must never become an output.
    destinations = {}
    reserve(destinations, declaration_path, "declaration input")
    skill_manifest = package_path("mocker", "guide-manifest.json")
    embedded_manifest = embedded_destination("manifest.json")
    reserve(destinations, skill_manifest, "skill manifest")
    reserve(destinations, embedded_manifest, "embedded manifest")
    indexed = {}
    paths = {}
    for source in declarations["sources"]:
        if not isinstance(source, dict):
            raise SystemExit("source must be an object")
        for field in ("topic", "source", "embedded", "workflowId"):
            if not isinstance(source.get(field), str) or not source[field]:
                if field == "source":
                    raise SystemExit("expected nonempty relative POSIX path for source")
                raise SystemExit(f"source requires nonempty {field}")
        topic = source["topic"]
        if topic in indexed:
            raise SystemExit(f"duplicate topic: {topic}")
        canonical_path = package_path(source.get("package", "mocker"), source["source"])
        embedded = embedded_path(source["embedded"])
        reserve(destinations, canonical_path, f"canonical topic {topic}")
        reserve(destinations, embedded, f"embedded topic {topic}")
        copies = source.get("copies", [])
        if not isinstance(copies, list):
            raise SystemExit(f"topic {topic}: copies must be a list")
        copy_paths = []
        for copy in copies:
            if not isinstance(copy, dict):
                raise SystemExit(f"topic {topic}: copy must be an object")
            path = package_path(copy.get("package"), copy.get("path"))
            reserve(destinations, path, f"copy of topic {topic}")
            copy_paths.append(path)
        paths[topic] = (canonical_path, embedded, copy_paths)
        indexed[topic] = source
    workflow_declarations = validate_workflows(declarations, indexed)

    sources = []
    texts = {}
    for source in declarations["sources"]:
        canonical_path = paths[source["topic"]][0]
        if not canonical_path.is_file():
            raise SystemExit(f"missing canonical source: {canonical_path}")
        text = without_identity(canonical_path.read_text())
        texts[source["topic"]] = text
        sources.append(
            dict(
                source,
                sourceHash=digest(text.encode()),
                contentHash=digest(body(text).encode()),
            )
        )
    base = {"sources": sources, "workflows": workflow_declarations}
    manifest_hash = digest(canonical(base))
    set_id = manifest_hash
    workflows = []
    outputs = {}
    for workflow in base["workflows"]:
        topics = [
            {"topic": s["topic"], "contentHash": s["contentHash"]}
            for s in sources
            if s["workflowId"] == workflow["workflowId"]
        ]
        workflows.append(
            dict(workflow, guideSetId=set_id, manifestHash=manifest_hash, topics=topics)
        )
    for source in sources:
        canonical_path, embedded, copy_paths = paths[source["topic"]]
        text = texts[source["topic"]]
        if text.startswith("---\n"):
            text = text.replace(
                "\n---\n",
                f'\n  guideSetId: "{set_id}"\n  manifestHash: "{manifest_hash}"\n---\n',
                1,
            )
            outputs[canonical_path] = text
        outputs[embedded] = text
        for path in copy_paths:
            outputs[path] = text
    for workflow in base["workflows"]:
        validate_metadata(
            outputs[paths[workflow["entrypoint"]][1]],
            workflow,
        )
    manifest = dict(
        base, guideSetId=set_id, manifestHash=manifest_hash, workflows=workflows
    )
    manifest_text = json.dumps(manifest, indent=2, ensure_ascii=False) + "\n"
    outputs[skill_manifest] = manifest_text
    outputs[embedded_manifest] = manifest_text
    stale = []
    for path, text in outputs.items():
        if check:
            if not path.exists() or path.read_text() != text:
                stale.append(str(path.relative_to(ROOT)))
        else:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(text)
    if stale:
        raise SystemExit("guide-sync drift: " + ", ".join(stale))
    print(set_id)


if __name__ == "__main__":
    generate("--check" in sys.argv)
