"""Run fictional-only terms inspection without personal configuration or services."""

import json
from pathlib import Path
import shutil
import subprocess
import tempfile


ROOT = Path(__file__).resolve().parents[1]
FIXTURES = ROOT / "testdata" / "terms-review-demo" / "wiki"


def main():
    print("FICTIONAL VALIDATION ONLY: these terms are not rights evidence for real material.", flush=True)
    with tempfile.TemporaryDirectory(prefix="cogvault-terms-demo-") as temporary:
        base = Path(temporary)
        wiki = base / "wiki"
        shutil.copytree(FIXTURES, wiki)
        config = base / "config.yaml"
        database = base / "unused.db"
        config.write_text(
            f"wiki_dir: {json.dumps(str(wiki))}\n"
            f"db_path: {json.dumps(str(database))}\n"
            "adapter: markdown\nsources: []\n",
            encoding="utf-8",
        )
        binary = base / "cogvault"
        subprocess.run(["go", "build", "-o", str(binary), "./cmd/cogvault"], cwd=ROOT, check=True)
        (wiki / "absent.md").write_text(
            "# Fictional missing record\nValidation only; not rights evidence for real material.\n",
            encoding="utf-8",
        )
        recorded = (wiki / "recorded.md").read_text(encoding="utf-8")
        (wiki / "incomplete.md").write_text(
            recorded.replace("    quote: Synthetic summaries may be submitted with attribution. Redistribution of the fictional original is prohibited.\n", ""),
            encoding="utf-8",
        )
        before = {path.name: path.read_bytes() for path in wiki.iterdir()}
        for name, expected in [
            ("recorded.md", "recorded"),
            ("unknown.md", "unverified"),
            ("ambiguous.md", "unverified"),
            ("absent.md", "unverified"),
            ("incomplete.md", "unverified"),
        ]:
            run = subprocess.run(
                [str(binary), "terms", name, "--config", str(config)],
                check=True, capture_output=True, text=True,
            )
            result = json.loads(run.stdout)
            if result["status"] != expected:
                raise RuntimeError(f"{name}: expected {expected}, got {result['status']}")
            print(run.stdout, end="", flush=True)
        after = {path.name: path.read_bytes() for path in wiki.iterdir()}
        if before != after or database.exists():
            raise RuntimeError("Inspector modified the wiki or created a database")
        print("PASS: five fictional CLI scenarios; wiki bytes unchanged; no database created.")
    print("Temporary config, wiki, and binary removed. No LLM or MCP service used.")


if __name__ == "__main__":
    main()
