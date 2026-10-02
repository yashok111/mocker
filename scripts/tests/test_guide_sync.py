"""Exercise the published guide generator in isolated repository trees."""

import json
import pathlib
import shutil
import subprocess
import tempfile
import unittest

REPO = pathlib.Path(__file__).resolve().parents[2]


def skill(name, workflow):
    metadata = {
        "workflowId": workflow["workflowId"],
        "workflowVersion": workflow["workflowVersion"],
        "requiredModelSchemaVersions": json.dumps(
            workflow["requiredModelSchemaVersions"]
        ),
        "requiredCapabilities": json.dumps(workflow["requiredCapabilities"]),
    }
    fields = "\n".join(
        f"  {key}: {json.dumps(value)}" for key, value in metadata.items()
    )
    return (
        f"---\nname: {name}\ndescription: Isolated fixture workflow.\n"
        f"metadata:\n{fields}\n---\n\n# {name}\n\nFixture instructions.\n"
    )


class GuideSyncPackagingTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = pathlib.Path(self.temporary.name)
        for directory in (
            "scripts",
            "skills/mocker",
            "skills/mocker-backend-import",
            "internal/guide",
        ):
            (self.root / directory).mkdir(parents=True)
        self.script = self.root / "scripts/guide-sync.py"
        shutil.copyfile(REPO / "scripts/guide-sync.py", self.script)
        routing = {
            "workflowId": "mocker-routing",
            "workflowVersion": "1",
            "entrypoint": "overview",
            "requiredModelSchemaVersions": [],
            "requiredCapabilities": [],
        }
        importing = {
            "workflowId": "mocker-backend-import",
            "workflowVersion": "2",
            "entrypoint": "backend-import",
            "requiredModelSchemaVersions": ["1"],
            "requiredCapabilities": ["backend-source-import"],
        }
        self.leaf = self.root / "skills/mocker-backend-import/SKILL.md"
        (self.root / "skills/mocker/SKILL.md").write_text(skill("mocker", routing))
        self.leaf.write_text(skill("mocker-backend-import", importing))
        self.declaration = self.root / "skills/mocker/guide-sources.json"
        self.alias = self.root / "skills/mocker/references/backend/import.md"
        self.declarations = {
            "sources": [
                {
                    "topic": "overview",
                    "source": "SKILL.md",
                    "embedded": "overview.md",
                    "workflowId": "mocker-routing",
                },
                {
                    "topic": "backend-import",
                    "package": "mocker-backend-import",
                    "source": "SKILL.md",
                    "embedded": "backend-import.md",
                    "workflowId": "mocker-backend-import",
                    "copies": [
                        {"package": "mocker", "path": "references/backend/import.md"}
                    ],
                },
            ],
            "workflows": [routing, importing],
        }

    def run_sync(self, *arguments):
        self.declaration.write_text(json.dumps(self.declarations))
        return subprocess.run(
            ["python3", str(self.script), *arguments],
            capture_output=True,
            text=True,
            check=False,
        )

    def generated_files(self):
        return {
            path.relative_to(self.root).as_posix(): path.read_bytes()
            for path in self.root.rglob("*")
            if path.is_file() and path not in (self.script, self.declaration)
        }

    def generate(self):
        result = self.run_sync()
        self.assertEqual(result.returncode, 0, result.stderr)
        return result.stdout.strip()

    def assert_invalid_without_changes(self, message):
        self.declaration.write_text(json.dumps(self.declarations))
        before = self.generated_files()
        directories = {
            p.relative_to(self.root) for p in self.root.rglob("*") if p.is_dir()
        }
        result = self.run_sync()
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn(message, result.stderr)
        self.assertEqual(self.generated_files(), before)
        self.assertEqual(
            {p.relative_to(self.root) for p in self.root.rglob("*") if p.is_dir()},
            directories,
            "validation failure created output directories",
        )

    def test_stale_live_owner_fails_before_output_changes(self):
        self.load_published_relational_sources()
        self.generate()
        topic = self.root / "skills/mocker/references/backend/profiles/go-sql.md"
        original = topic.read_text()
        for label in (
            "database v1",
            "database1",
            "database workflow1",
            "`mocker-backend-database` workflow1",
        ):
            with self.subTest(label=label):
                topic.write_text(original.replace("database v5", label))
                self.assert_invalid_without_changes(
                    "stale live guide owner database1; expected database5"
                )
        topic.write_text(original)

    def test_stale_owner_dependency_table_fails_before_output_changes(self):
        self.load_published_relational_sources()
        self.generate()
        topic = self.root / "skills/mocker-backend-import/SKILL.md"
        original = topic.read_text()
        stale = original.replace(
            "| mocker-backend-inspect | 5 |", "| mocker-backend-inspect | 1 |"
        )
        self.assertNotEqual(stale, original, "owner mutation must change the fixture")
        topic.write_text(stale)
        self.assert_invalid_without_changes(
            "stale live guide owner inspect1; expected inspect5"
        )

    def test_explicit_historical_owner_preserves_prose(self):
        self.generate()
        historical = (
            "Historical import1 was released earlier. <!-- guide-owner-history -->"
        )
        self.leaf.write_text(
            self.leaf.read_text()
            + "\n"
            + historical
            + "\nOpaque dataBase64 identifier.\n"
        )
        self.generate()
        self.assertIn(historical, self.alias.read_text())

    def test_unmarked_historical_owner_is_rejected(self):
        self.generate()
        self.leaf.write_text(
            self.leaf.read_text() + "\nHistorical import1 was released earlier.\n"
        )
        self.assert_invalid_without_changes(
            "stale live guide owner import1; expected import2"
        )

    def test_leaf_and_legacy_copy_share_generated_identity(self):
        identity = self.generate()
        leaf = self.leaf.read_bytes()
        self.assertEqual(leaf, self.alias.read_bytes())
        self.assertEqual(
            leaf, (self.root / "internal/guide/backend-import.md").read_bytes()
        )
        self.assertIn(f'guideSetId: "{identity}"'.encode(), leaf)
        self.assertEqual(self.run_sync("--check").returncode, 0)

    def load_published_relational_sources(self):
        self.declarations = json.loads(
            (REPO / "skills/mocker/guide-sources.json").read_text()
        )
        for source in self.declarations["sources"]:
            relative = (
                pathlib.Path("skills")
                / source.get("package", "mocker")
                / source["source"]
            )
            destination = self.root / relative
            destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(REPO / relative, destination)

    def test_relational_leaves_and_shared_topics_have_distinct_owners(self):
        self.load_published_relational_sources()
        self.generate()
        manifest = json.loads((self.root / "internal/guide/manifest.json").read_text())
        self.assertEqual(len(manifest["sources"]), 20)
        workflows = {w["workflowId"]: w for w in manifest["workflows"]}
        self.assertEqual(len(workflows), 5)
        importing = workflows["mocker-backend-import"]
        database = workflows["mocker-backend-database"]
        self.assertEqual(importing["workflowVersion"], "5")
        self.assertEqual(importing["requiredModelSchemaVersions"], ["1", "2", "3", "4"])
        self.assertEqual(database["requiredModelSchemaVersions"], ["2", "3", "4"])
        self.assertIn(
            "backend-database-reference",
            [topic["topic"] for topic in database["topics"]],
        )
        self.assertIn(
            "backend-model", [topic["topic"] for topic in importing["topics"]]
        )
        leaf = (self.root / "skills/mocker-backend-database/SKILL.md").read_bytes()
        self.assertEqual(
            leaf,
            (
                self.root / "skills/mocker/references/backend/database-workflow.md"
            ).read_bytes(),
        )
        self.assertEqual(
            leaf, (self.root / "internal/guide/backend-database.md").read_bytes()
        )
        self.assertNotEqual(
            leaf,
            (self.root / "skills/mocker/references/backend/database.md").read_bytes(),
        )
        self.assertEqual(self.run_sync("--check").returncode, 0)

    def test_editor_owner_mutation_changes_set_and_detects_embedded_drift(self):
        self.load_published_relational_sources()
        identity = self.generate()
        path = self.root / "skills/mocker/references/backend/editor-projections.md"
        embedded = self.root / "internal/guide/backend-editor-projections.md"
        manifest = json.loads((self.root / "internal/guide/manifest.json").read_text())
        source = next(s for s in manifest["sources"] if s["topic"] == "backend-editor-projections")
        self.assertEqual(source["workflowId"], "mocker-backend-inspect")
        original_hash = source["contentHash"]
        path.write_text(path.read_text() + "\nSynthetic changed editor procedure.\n")
        before = self.generated_files()
        self.assertNotEqual(self.run_sync("--check").returncode, 0)
        self.assertEqual(self.generated_files(), before)
        changed_identity = self.generate()
        self.assertNotEqual(changed_identity, identity)
        self.assertEqual(path.read_bytes(), embedded.read_bytes())
        manifest = json.loads((self.root / "internal/guide/manifest.json").read_text())
        owner = next(w for w in manifest["workflows"] if w["workflowId"] == "mocker-backend-inspect")
        topic = next(t for t in owner["topics"] if t["topic"] == "backend-editor-projections")
        self.assertNotEqual(topic["contentHash"], original_hash)
        for workflow in manifest["workflows"]:
            self.assertEqual(workflow["guideSetId"], changed_identity)
            self.assertEqual(workflow["manifestHash"], changed_identity)
        embedded.write_text("Synthetic embedded drift.\n")
        before = self.generated_files()
        failed = self.run_sync("--check")
        self.assertNotEqual(failed.returncode, 0)
        self.assertIn("backend-editor-projections.md", failed.stderr)
        self.assertEqual(self.generated_files(), before)
        self.generate()
        self.assertEqual(path.read_bytes(), embedded.read_bytes())

    def test_runtime_inspect_leaf_and_references_have_one_pinned_owner(self):
        self.load_published_relational_sources()
        identity = self.generate()
        manifest = json.loads((self.root / "internal/guide/manifest.json").read_text())
        owner = next(
            w
            for w in manifest["workflows"]
            if w["workflowId"] == "mocker-backend-inspect"
        )
        self.assertEqual(owner["workflowVersion"], "5")
        self.assertEqual(owner["requiredModelSchemaVersions"], ["3", "4"])
        self.assertEqual(owner["guideSetId"], identity)
        self.assertEqual(
            {t["topic"] for t in owner["topics"]},
            {"backend-inspect", "backend-flow-reference", "backend-analysis", "backend-editor-projections"},
        )
        leaf = (self.root / "skills/mocker-backend-inspect/SKILL.md").read_bytes()
        self.assertEqual(
            leaf,
            (self.root / "skills/mocker/references/backend/inspect.md").read_bytes(),
        )
        self.assertEqual(
            leaf, (self.root / "internal/guide/backend-inspect.md").read_bytes()
        )
        importer = (self.root / "skills/mocker-backend-import/SKILL.md").read_text()
        self.assertIn(
            "| `backend-flow-reference` / `backend-analysis` / `backend-editor-projections` | inspect v5 |", importer
        )
        inspector = leaf.decode()
        self.assertIn("Inspect workflow1 was released", inspector)
        self.assertNotIn("no released older inspect version", inspector)
        self.assertEqual(
            owner["requiredViewSchemaVersions"],
            ["saved-view-v1", "api-artifact-pins-v1", "backend-editor-artifacts-v1"],
        )
        for capability in (
            "backend-flow-query",
            "backend-data-access-query",
            "backend-saved-views",
            "backend-api-artifact-pins",
        ):
            self.assertIn(capability, owner["requiredCapabilities"])
        self.assertEqual(self.run_sync("--check").returncode, 0)

    def test_shared_database_reference_changes_both_leaf_identities(self):
        self.load_published_relational_sources()
        previous = self.generate()
        reference = self.root / "skills/mocker/references/backend/database.md"
        reference.write_text(reference.read_text() + "\nChanged inspection guidance.\n")
        current = self.generate()
        self.assertNotEqual(current, previous)
        for package in (
            "mocker",
            "mocker-backend-import",
            "mocker-backend-database",
            "mocker-backend-inspect",
        ):
            text = (self.root / "skills" / package / "SKILL.md").read_text()
            self.assertIn(f'guideSetId: "{current}"', text)
            self.assertNotIn(f'guideSetId: "{previous}"', text)
        self.assertEqual(self.run_sync("--check").returncode, 0)

    def test_database_alias_cannot_overwrite_shared_reference(self):
        self.load_published_relational_sources()
        self.generate()
        database = next(
            source
            for source in self.declarations["sources"]
            if source["topic"] == "backend-database"
        )
        database["copies"][0]["path"] = "references/backend/database.md"
        self.assert_invalid_without_changes("destination collision")

    def test_default_package_preserves_root_only_declarations(self):
        self.declarations["sources"] = self.declarations["sources"][:1]
        self.declarations["workflows"] = self.declarations["workflows"][:1]
        self.generate()
        self.assertEqual(
            (self.root / "skills/mocker/SKILL.md").read_bytes(),
            (self.root / "internal/guide/overview.md").read_bytes(),
        )
        self.assertEqual(self.run_sync("--check").returncode, 0)

    def test_repeat_generation_changes_no_bytes(self):
        identity = self.generate()
        before = self.generated_files()
        self.assertEqual(self.generate(), identity)
        self.assertEqual(self.generated_files(), before)

    def test_source_change_changes_set_and_alias(self):
        identity = self.generate()
        self.leaf.write_text(self.leaf.read_text() + "\nChanged procedure.\n")
        self.assertNotEqual(self.generate(), identity)
        self.assertEqual(self.leaf.read_bytes(), self.alias.read_bytes())

    def test_copy_declaration_changes_set_without_adding_topic(self):
        identity = self.generate()
        self.declarations["sources"][1]["copies"].append(
            {"package": "mocker", "path": "references/second-import.md"}
        )
        self.assertNotEqual(self.generate(), identity)
        manifest = json.loads((self.root / "internal/guide/manifest.json").read_text())
        self.assertEqual(len(manifest["sources"]), 2)
        self.assertEqual(len(manifest["workflows"][1]["topics"]), 1)

    def test_check_detects_alias_drift_without_mutating_files(self):
        self.generate()
        self.alias.write_text("Drifted alias.\n")
        before = self.generated_files()
        result = self.run_sync("--check")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("references/backend/import.md", result.stderr)
        self.assertEqual(self.generated_files(), before)

    def test_missing_canonical_source_fails_before_output_changes(self):
        self.generate()
        self.leaf.unlink()
        self.assert_invalid_without_changes("missing canonical source")

    def test_metadata_mismatch_fails_before_output_changes(self):
        self.generate()
        self.leaf.write_text(
            self.leaf.read_text().replace(
                'workflowVersion: "2"', 'workflowVersion: "99"'
            )
        )
        self.assert_invalid_without_changes("incorrect workflowVersion")

    def test_duplicate_topic_fails_before_output_changes(self):
        self.generate()
        self.declarations["sources"][1]["topic"] = "overview"
        self.assert_invalid_without_changes("duplicate topic")

    def test_duplicate_embedded_destination_fails_before_output_changes(self):
        self.generate()
        self.declarations["sources"][1]["embedded"] = "overview.md"
        self.assert_invalid_without_changes("destination collision")

    def test_copy_cannot_alias_another_canonical_source(self):
        self.generate()
        self.declarations["sources"][1]["copies"][0]["path"] = "SKILL.md"
        self.assert_invalid_without_changes("destination collision")

    def test_copy_cannot_alias_itself_via_symlink(self):
        self.generate()
        link = self.root / "skills/mocker-backend-import/import-owner.md"
        link.symlink_to(self.leaf)
        self.declarations["sources"][1]["copies"][0]["package"] = (
            "mocker-backend-import"
        )
        self.declarations["sources"][1]["copies"][0]["path"] = "import-owner.md"
        self.assert_invalid_without_changes("destination collision")

    def test_copy_cannot_be_declared_as_another_canonical_source(self):
        self.generate()
        self.declarations["sources"].append(
            {
                "topic": "duplicate-import",
                "source": "references/backend/import.md",
                "embedded": "duplicate-import.md",
                "workflowId": "mocker-backend-import",
            }
        )
        self.assert_invalid_without_changes("destination collision")

    def test_output_file_cannot_also_be_another_outputs_directory(self):
        self.generate()
        self.declarations["sources"][1]["copies"] = [
            {"package": "mocker", "path": "new-output.md"},
            {"package": "mocker", "path": "new-output.md/nested.md"},
        ]
        self.assert_invalid_without_changes("destination collision")

    def test_canonical_source_cannot_be_a_reserved_manifest(self):
        self.generate()
        self.declarations["sources"].append(
            {
                "topic": "reserved-source",
                "source": "guide-manifest.json",
                "embedded": "reserved-source.md",
                "workflowId": "mocker-routing",
            }
        )
        self.assert_invalid_without_changes("destination collision")

    def test_copy_cannot_write_a_reserved_manifest(self):
        self.generate()
        self.declarations["sources"][1]["copies"][0]["path"] = "guide-manifest.json"
        self.assert_invalid_without_changes("destination collision")

    def test_copy_cannot_write_declaration_input(self):
        self.generate()
        self.declarations["sources"][1]["copies"][0]["path"] = "guide-sources.json"
        self.assert_invalid_without_changes("destination collision")

    def test_canonical_source_cannot_write_declaration_input(self):
        self.generate()
        self.declarations["sources"].append(
            {
                "topic": "reserved-source",
                "source": "guide-sources.json",
                "embedded": "reserved-source.md",
                "workflowId": "mocker-routing",
            }
        )
        self.assert_invalid_without_changes("destination collision")

    def test_reserved_input_alias_is_detected_by_resolved_path(self):
        self.generate()
        link = self.root / "skills/mocker/declaration-alias.md"
        link.symlink_to(self.declaration)
        self.declarations["sources"][1]["copies"][0]["path"] = "declaration-alias.md"
        self.assert_invalid_without_changes("destination collision")

    def test_invalid_source_and_copy_paths_fail_before_output_changes(self):
        self.generate()
        for path in ("/absolute.md", "../escaped.md", "a/./bad.md", "a//bad.md", ""):
            for target in ("source", "copy"):
                with self.subTest(path=path, target=target):
                    original = json.loads(json.dumps(self.declarations))
                    if target == "source":
                        self.declarations["sources"][1]["source"] = path
                    else:
                        self.declarations["sources"][1]["copies"][0]["path"] = path
                    self.assert_invalid_without_changes("relative POSIX path")
                    self.declarations = original

    def test_invalid_package_names_fail_before_output_changes(self):
        self.generate()
        for package in ("../mocker", "Mocker", "mocker--import", "", "mocker/import"):
            with self.subTest(package=package):
                self.declarations["sources"][1]["package"] = package
                self.assert_invalid_without_changes("invalid package")

    def test_embedded_paths_are_plain_markdown_filenames(self):
        self.generate()
        for filename in (
            "../escaped.md",
            "/escaped.md",
            "nested/topic.md",
            "topic.json",
        ):
            with self.subTest(filename=filename):
                self.declarations["sources"][1]["embedded"] = filename
                self.assert_invalid_without_changes("plain .md filename")

    def test_symlink_escape_fails_before_output_changes(self):
        self.generate()
        outside = self.root / "outside"
        outside.mkdir()
        (outside / "guide.md").write_text("# Outside\n")
        link = self.root / "skills/mocker-backend-import/escape"
        link.symlink_to(outside, target_is_directory=True)
        self.declarations["sources"][1]["source"] = "escape/guide.md"
        self.assert_invalid_without_changes("escapes package")

    def test_embedded_symlink_escape_fails_before_output_changes(self):
        self.generate()
        outside = self.root / "outside.md"
        outside.write_text("# Outside\n")
        (self.root / "internal/guide/backend-import.md").unlink()
        (self.root / "internal/guide/backend-import.md").symlink_to(outside)
        self.assert_invalid_without_changes("escapes embedded directory")

    def test_invalid_workflow_fails_before_output_changes(self):
        self.generate()
        self.declarations["workflows"][1]["entrypoint"] = "unavailable"
        self.assert_invalid_without_changes("unknown entrypoint")

    def test_missing_output_directories_are_created_only_after_validation(self):
        shutil.rmtree(self.root / "internal/guide")
        self.generate()
        self.assertTrue(self.alias.is_file())
        self.assertTrue((self.root / "internal/guide/manifest.json").is_file())


if __name__ == "__main__":
    unittest.main()
