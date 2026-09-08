#!/usr/bin/env python3
"""Exercise online backup snapshots against a live WAL database."""

from pathlib import Path
import sqlite3
import subprocess
import sys
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("snapshot-live.py")


class SnapshotLiveTest(unittest.TestCase):
    def test_copies_wal_database_as_standalone_consistent_sqlite(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / "source"
            source.mkdir()
            database = source / "state.sqlite"
            config = source / "settings.json"
            config.write_text('{"enabled":true}\n', encoding="utf-8")
            cache = source / "cache"
            cache.mkdir()
            (cache / "derived.bin").write_bytes(b"derived")
            config.chmod(0o640)
            connection = sqlite3.connect(database)
            self.addCleanup(connection.close)
            connection.execute("PRAGMA journal_mode = WAL")
            connection.execute("PRAGMA wal_autocheckpoint = 0")
            connection.execute("CREATE TABLE records (value TEXT NOT NULL)")
            connection.execute("INSERT INTO records VALUES ('committed-in-wal')")
            connection.commit()
            self.assertTrue(database.with_name(database.name + "-wal").exists())

            output = root / "snapshot"
            result = subprocess.run(
                [sys.executable, str(SCRIPT), "--output", str(output), "--exclude", str(cache), str(source)],
                capture_output=True,
                text=True,
                timeout=30,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            copied_source = output / source.absolute().relative_to("/")
            copied_database = copied_source / database.name
            self.assertFalse(copied_database.with_name(copied_database.name + "-wal").exists())
            with sqlite3.connect(copied_database) as restored:
                self.assertEqual(restored.execute("SELECT value FROM records").fetchall(), [("committed-in-wal",)])
                self.assertEqual(restored.execute("PRAGMA quick_check").fetchone(), ("ok",))
            self.assertEqual((copied_source / config.name).read_text(encoding="utf-8"), config.read_text(encoding="utf-8"))
            self.assertEqual((copied_source / config.name).stat().st_mode & 0o777, 0o640)
            self.assertTrue((output / ".embymedia-online-backup.json").is_file())
            self.assertFalse((copied_source / cache.name).exists())

    def test_rejects_output_below_a_source(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            source = Path(temporary)
            result = subprocess.run(
                [sys.executable, str(SCRIPT), "--output", str(source / "snapshot"), str(source)],
                capture_output=True,
                text=True,
                timeout=30,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("outside source", result.stderr)


if __name__ == "__main__":
    unittest.main()
