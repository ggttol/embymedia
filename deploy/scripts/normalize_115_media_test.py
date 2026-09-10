import collections
import importlib.util
import io
import json
import pathlib
import sqlite3
import tempfile
import types
import unittest

SCRIPT = pathlib.Path(__file__).with_name("normalize-115-media.py")
SPEC = importlib.util.spec_from_file_location("normalize_115_media", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(MODULE)


class NormalizationPlannerTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.root = pathlib.Path(self.temporary.name)
        self.inventory = self.root / "inventory.sqlite"
        self.emby = self.root / "emby.sqlite"
        self.strm = self.root / "strm.sqlite"
        self.layout = self.root / "layout.json"
        self.plan = self.root / "plan.sqlite"
        self._write_inventory()
        self._write_emby()
        self._write_strm()
        self.layout.write_text(json.dumps({
            "provider_root": {"account_id": "default", "cid": "root", "name": "emby"},
            "library_order": ["IMAX巨幕", "电视剧"],
        }))

    def tearDown(self):
        self.temporary.cleanup()

    def _write_inventory(self):
        database = sqlite3.connect(self.inventory)
        database.execute("CREATE TABLE nodes(file_id TEXT PRIMARY KEY,parent_id TEXT,name TEXT,relative_path TEXT,top_library TEXT,depth INTEGER,is_folder INTEGER,size INTEGER,sha1 TEXT,pick_code TEXT)")
        database.executemany("INSERT INTO nodes VALUES(?,?,?,?,?,?,?,?,?,?)", [
            ("root", "0", "emby", "", "", 0, 1, 0, "", ""),
            ("movies", "root", "电影", "电影", "电影", 1, 1, 0, "", ""),
            ("shows", "root", "电视剧", "电视剧", "电视剧", 1, 1, 0, "", ""),
            ("movie-a", "movies", "Movie.A.mkv", "", "电影", 2, 0, 50_000_000_000, "MOVIE", ""),
            ("movie-b", "movies", "Movie.B.mkv", "", "电影", 2, 0, 50_000_000_000, "MOVIE", ""),
            ("episode-a", "shows", "Show.S01E01.mkv", "", "电视剧", 2, 0, 100, "EPISODE", ""),
            ("episode-b", "shows", "Show.S01E02.mkv", "", "电视剧", 2, 0, 100, "EPISODE", ""),
        ])
        database.commit()
        database.close()

    def _write_emby(self):
        database = sqlite3.connect(self.emby)
        database.executescript("""
            CREATE TABLE libraries(id TEXT PRIMARY KEY,name TEXT,collection_type TEXT,locations_json TEXT,image_tag TEXT);
            CREATE TABLE items(id TEXT PRIMARY KEY,library_id TEXT,parent_id TEXT,series_id TEXT,season_id TEXT,type TEXT,name TEXT,path TEXT,production_year INTEGER,index_number INTEGER,parent_index_number INTEGER,provider_ids_json TEXT,media_sources_json TEXT,media_streams_json TEXT,image_tags_json TEXT,raw_json TEXT);
        """)
        database.executemany("INSERT INTO libraries VALUES(?,?,?,?,?)", [
            ("movies", "电影", "movies", "[]", ""),
            ("shows", "电视剧", "tvshows", "[]", ""),
        ])
        def item(item_id, library_id, item_type, name, path, tmdb, year, series_id="", episode=None):
            raw = {"Genres": [], "Bitrate": 50_000_000 if item_type == "Movie" else 1_000_000}
            if episode is not None:
                raw["IndexNumberEnd"] = None
            return (item_id, library_id, "", series_id, "", item_type, name, path, year,
                    episode, 1 if episode is not None else None, json.dumps({"Tmdb": tmdb}), "[]", "[]", "{}", json.dumps(raw))
        database.executemany("INSERT INTO items VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)", [
            item("movie-a", "movies", "Movie", "示例电影 4K原盘REMUX", "/strm/电影/Movie.A.strm", "1", 2025),
            item("movie-b", "movies", "Movie", "示例电影 4K原盘REMUX", "/strm/电影/Movie.B.strm", "1", 2025),
            item("series", "shows", "Series", "示例剧", "/strm/电视剧/Show", "2", 2024),
            item("episode-a", "shows", "Episode", "第一集", "/strm/电视剧/Show.S01E01.strm", "", 2024, "series", 1),
            item("episode-b", "shows", "Episode", "第二集", "/strm/电视剧/Show.S01E02.strm", "", 2024, "series", 2),
        ])
        database.commit()
        database.close()

    def _write_strm(self):
        database = sqlite3.connect(self.strm)
        database.execute("CREATE TABLE strm(relative_path TEXT PRIMARY KEY,target TEXT,size INTEGER)")
        database.executemany("INSERT INTO strm VALUES(?,?,?)", [
            ("电影/Movie.A.strm", "/media/电影/Movie.A.mkv", 1),
            ("电影/Movie.B.strm", "/media/电影/Movie.B.mkv", 1),
            ("电视剧/Show.S01E01.strm", "/media/电视剧/Show.S01E01.mkv", 1),
            ("电视剧/Show.S01E02.strm", "/media/电视剧/Show.S01E02.mkv", 1),
        ])
        database.commit()
        database.close()

    def test_plan_stages_only_identity_equivalent_exact_duplicates(self):
        args = types.SimpleNamespace(inventory=str(self.inventory), emby=str(self.emby), strm=str(self.strm), layout=str(self.layout), output=str(self.plan))
        MODULE.command_plan(args)
        database = sqlite3.connect(self.plan)
        dispositions = dict(database.execute("SELECT disposition,COUNT(*) FROM operations GROUP BY disposition"))
        self.assertEqual(dispositions, {"canonical": 1, "exact_duplicate": 1})
        self.assertEqual(database.execute("SELECT COUNT(*) FROM findings WHERE reason='exact_bytes_have_conflicting_identities'").fetchone()[0], 2)
        title = database.execute("SELECT name FROM directories WHERE key='title:IMAX巨幕:movie:1'").fetchone()[0]
        self.assertEqual(title, "示例电影 (2025) [tmdbid=1]")
        database.close()

    def test_plan_includes_reviewed_provider_only_disc(self):
        database = sqlite3.connect(self.inventory)
        database.execute("INSERT INTO nodes VALUES(?,?,?,?,?,?,?,?,?,?)", ("disc", "shows", "DISC1.iso", "", "电视剧", 2, 0, 50, "DISC", ""))
        database.commit()
        database.close()
        layout = json.loads(self.layout.read_text())
        layout["file_identity_overrides"] = {
            "disc": {"tmdb_id": "3", "title": "纪录片", "year": 2020, "destination": "电视剧", "season": 1, "episode": 1, "episode_end": 4}
        }
        self.layout.write_text(json.dumps(layout))
        args = types.SimpleNamespace(inventory=str(self.inventory), emby=str(self.emby), strm=str(self.strm), layout=str(self.layout), output=str(self.plan))
        MODULE.command_plan(args)
        database = sqlite3.connect(self.plan)
        self.assertEqual(database.execute("SELECT target_name FROM operations WHERE object_id='disc'").fetchone()[0], "纪录片 S01E01-E04.iso")
        database.close()

    def test_long_episode_title_retains_numbering_suffix(self):
        owner = {"name": "长" * 200, "type": "Series"}
        item = {"type": "Episode", "parent_index_number": 1, "index_number": 2, "raw_json": "{}"}
        name = MODULE.canonical_media_name(item, owner, "Original", ".mkv", False)
        self.assertLessEqual(len(name.encode("utf-8")), 250)
        self.assertTrue(name.endswith(" S01E02.mkv"), name)

    def test_provider_request_stops_immediately_on_405(self):
        original_open, original_sleep = MODULE.urllib.request.urlopen, MODULE.time.sleep
        MODULE.urllib.request.urlopen = lambda *_args, **_kwargs: (_ for _ in ()).throw(MODULE.urllib.error.HTTPError("https://115", 405, "limited", {}, None))
        MODULE.time.sleep = lambda _seconds: self.fail("405 response was retried")
        try:
            with self.assertRaisesRegex(RuntimeError, "refresh the account cookie"):
                MODULE.provider_request("cookie", "GET", "https://115")
        finally:
            MODULE.urllib.request.urlopen, MODULE.time.sleep = original_open, original_sleep

    def test_provider_request_retries_truncated_response(self):
        attempts = 0
        original_open, original_sleep = MODULE.urllib.request.urlopen, MODULE.time.sleep
        def fake_open(*_args, **_kwargs):
            nonlocal attempts
            attempts += 1
            if attempts == 1:
                raise MODULE.http.client.IncompleteRead(b"", 10)
            return io.BytesIO(b'{"state":true}')
        MODULE.urllib.request.urlopen = fake_open
        MODULE.time.sleep = lambda _seconds: None
        try:
            self.assertEqual(MODULE.provider_request("cookie", "GET", "https://115"), {"state": True})
        finally:
            MODULE.urllib.request.urlopen, MODULE.time.sleep = original_open, original_sleep
        self.assertEqual(attempts, 2)

    def test_list_directory_deduplicates_overlapping_pages(self):
        pages = iter([
            {"state": True, "count": 3, "data": [{"fid": "a", "n": "a.mkv", "s": 1}, {"fid": "b", "n": "b.mkv", "s": 2}]},
            {"state": True, "count": 3, "data": [{"fid": "b", "n": "b.mkv", "s": 2}, {"fid": "c", "n": "c.mkv", "s": 3}]},
        ])
        original = MODULE.provider_request
        MODULE.provider_request = lambda *_args, **_kwargs: next(pages)
        try:
            files = MODULE.list_directory("cookie", "parent")
        finally:
            MODULE.provider_request = original
        self.assertEqual([file["id"] for file in files], ["a", "b", "c"])

    def test_apply_requires_drained_maintenance_state(self):
        database_path = self.root / "service.sqlite"
        database = sqlite3.connect(database_path)
        database.executescript("""
            CREATE TABLE scheduled_tasks(status TEXT);
            CREATE TABLE async_tasks(status TEXT);
            CREATE TABLE drive_accounts(cookie TEXT,is_default INTEGER,created_at TEXT);
            INSERT INTO scheduled_tasks VALUES('idle');
            INSERT INTO drive_accounts VALUES('secret',1,'2026-01-01');
        """)
        database.commit()
        database.close()
        original = MODULE.SOURCE_DATABASE
        MODULE.SOURCE_DATABASE = str(database_path)
        try:
            with self.assertRaisesRegex(RuntimeError, "enabled schedules=1"):
                MODULE.load_cookie_and_assert_maintenance()
        finally:
            MODULE.SOURCE_DATABASE = original

    def test_prepare_directories_uses_frozen_parent_identity(self):
        plan_path = self.root / "directories.sqlite"
        plan = MODULE.initialize_plan(plan_path)
        with plan:
            plan.execute("INSERT INTO directories(key,parent_key,name,cid,status) VALUES('root','','emby','root','ready')")
            plan.execute("INSERT INTO directories(key,parent_key,name) VALUES('library:电影','root','电影')")
            plan.execute("INSERT INTO directories(key,parent_key,name) VALUES('library:IMAX巨幕','root','IMAX巨幕')")
        plan.close()
        originals = MODULE.load_cookie_and_assert_maintenance, MODULE.mkdir, MODULE.find_directory
        MODULE.load_cookie_and_assert_maintenance = lambda: "cookie"
        MODULE.mkdir = lambda *_args: (_ for _ in ()).throw(MODULE.ProviderRejected("该目录名称已存在。"))
        MODULE.find_directory = lambda _cookie, parent, name: "recovered" if (parent, name) == ("root", "IMAX巨幕") else "unexpected"
        try:
            args = types.SimpleNamespace(inventory=str(self.inventory), plan=str(plan_path), delay=0, workers=1)
            MODULE.command_prepare_directories(args)
        finally:
            MODULE.load_cookie_and_assert_maintenance, MODULE.mkdir, MODULE.find_directory = originals
        database = sqlite3.connect(plan_path)
        self.assertEqual(database.execute("SELECT cid,status FROM directories WHERE key='library:电影'").fetchone(), ("movies", "ready"))
        self.assertEqual(database.execute("SELECT cid,status FROM directories WHERE key='library:IMAX巨幕'").fetchone(), ("recovered", "ready"))
        database.close()

    def test_residual_planner_preserves_configured_root(self):
        database = sqlite3.connect(self.inventory)
        database.execute("INSERT INTO nodes VALUES(?,?,?,?,?,?,?,?,?,?)", ("temporary", "root", "_迁移暂存", "", "_迁移暂存", 1, 1, 0, "", ""))
        database.commit()
        database.close()
        plan_path = self.root / "residual.sqlite"
        plan = MODULE.initialize_plan(plan_path)
        with plan:
            plan.execute("INSERT INTO metadata VALUES('root_cid','root')")
            plan.execute("INSERT INTO metadata VALUES('layout_json',?)", (json.dumps({"library_order": ["电影"], "reserved_directories": ["_迁移暂存"]}),))
            plan.execute("INSERT INTO directories(key,parent_key,name,cid,status) VALUES('root','','emby','root','ready')")
            plan.execute("INSERT INTO directories(key,parent_key,name,cid,status) VALUES('library:_待整理','root','_待整理','review','ready')")
            plan.execute("INSERT INTO directories(key,parent_key,name,cid,status) VALUES('library:电影','root','电影','movies','ready')")
        plan.close()
        MODULE.command_augment_residuals(types.SimpleNamespace(inventory=str(self.inventory), plan=str(plan_path)))
        database = sqlite3.connect(plan_path)
        self.assertEqual(database.execute("SELECT COUNT(*) FROM operations WHERE object_id='temporary'").fetchone()[0], 0)
        database.close()


    def test_cloud_drive_duplicate_staging_moves_and_records(self):
        media = self.root / "media"
        source = media / "电影" / "Duplicate.mkv"
        source.parent.mkdir(parents=True)
        source.write_bytes(b"dup")
        (media / "_待回收").mkdir()
        inventory_path = self.root / "duplicate-inventory.sqlite"
        inventory = sqlite3.connect(inventory_path)
        inventory.execute("CREATE TABLE nodes(file_id TEXT PRIMARY KEY,parent_id TEXT,name TEXT,relative_path TEXT,top_library TEXT,depth INTEGER,is_folder INTEGER,size INTEGER,sha1 TEXT,pick_code TEXT)")
        inventory.executemany("INSERT INTO nodes VALUES(?,?,?,?,?,?,?,?,?,?)", [
            ("root", "0", "emby", "", "", 0, 1, 0, "", ""),
            ("movies", "root", "电影", "", "电影", 1, 1, 0, "", ""),
            ("duplicate", "movies", "Duplicate.mkv", "", "电影", 2, 0, 3, "SHA", ""),
        ])
        inventory.commit()
        inventory.close()
        plan_path = self.root / "duplicate-plan.sqlite"
        plan = MODULE.initialize_plan(plan_path)
        with plan:
            plan.execute("INSERT INTO metadata VALUES('root_cid','root')")
            plan.execute("INSERT INTO directories(key,parent_key,name,cid,status) VALUES('library:_待回收','root','_待回收','recycle','ready')")
            plan.execute("INSERT INTO operations(phase,object_id,expected_parent_id,expected_name,expected_size,expected_sha1,target_directory_key,target_name,identity,disposition) VALUES(4,'duplicate','movies','Duplicate.mkv',3,'SHA','library:_待回收','SHA-duplicate.mkv','movie:1','exact_duplicate')")
        plan.close()
        original = MODULE.os.path.ismount
        MODULE.os.path.ismount = lambda _path: True
        try:
            MODULE.command_stage_duplicates_fs(types.SimpleNamespace(plan=str(plan_path), inventory=str(inventory_path), media_root=str(media), workers=1, batch_size=1))
        finally:
            MODULE.os.path.ismount = original
        self.assertFalse(source.exists())
        self.assertEqual((media / "_待回收" / "SHA-duplicate.mkv").read_bytes(), b"dup")
        plan = sqlite3.connect(plan_path)
        self.assertEqual(plan.execute("SELECT status FROM operations").fetchone()[0], "completed")
        plan.close()

    def test_apply_moves_renames_verifies_and_resumes(self):
        plan_path = self.root / "apply.sqlite"
        plan = MODULE.initialize_plan(plan_path)
        with plan:
            plan.execute("INSERT INTO directories(key,parent_key,name,cid,status) VALUES('root','','emby','root','ready')")
            plan.execute("INSERT INTO directories(key,parent_key,name) VALUES('library:电影','root','电影')")
            plan.execute("INSERT INTO operations(phase,object_id,expected_parent_id,expected_name,expected_size,expected_sha1,target_directory_key,target_name,identity,disposition) VALUES(3,'file','source','old.mkv',10,'SHA','library:电影','new.mkv','movie:1','canonical')")
        state = {
            "root": {},
            "source": {"file": {"id": "file", "name": "old.mkv", "folder": False, "size": 10, "sha1": "SHA"}},
        }
        calls = collections.Counter()
        def fake_list(_cookie, cid):
            return list(state[cid].values())
        def fake_get(_cookie, object_id):
            for parent_id, items in state.items():
                if object_id in items:
                    return {**items[object_id], "parent_id": parent_id}
            raise RuntimeError("missing test object")
        def fake_locate(_cookie, operation, _target, verify_parent=True):
            return fake_get(_cookie, operation["object_id"])
        def fake_mkdir(_cookie, parent, name):
            calls["mkdir"] += 1
            state[parent]["target"] = {"id": "target", "name": name, "folder": True, "size": 0, "sha1": ""}
            state["target"] = {}
            return "target"
        def fake_move(_cookie, object_ids, target):
            calls["move"] += 1
            for object_id in object_ids:
                state[target][object_id] = state["source"].pop(object_id)
        def fake_rename(_cookie, object_id, name):
            calls["rename"] += 1
            state["target"][object_id]["name"] = name
        originals = (MODULE.load_cookie_and_assert_maintenance, MODULE.list_directory, MODULE.get_object, MODULE.locate_object, MODULE.mkdir, MODULE.move, MODULE.rename)
        MODULE.load_cookie_and_assert_maintenance = lambda: "cookie"
        MODULE.list_directory = fake_list
        MODULE.get_object = fake_get
        MODULE.locate_object = fake_locate
        MODULE.mkdir = fake_mkdir
        MODULE.move = fake_move
        MODULE.rename = fake_rename
        try:
            args = types.SimpleNamespace(plan=str(plan_path), delay=0, workers=8)
            MODULE.command_apply(args)
            self.assertEqual(state["target"]["file"]["name"], "new.mkv")
            self.assertEqual(calls, {"mkdir": 1, "move": 1, "rename": 1})
            with plan:
                plan.execute("UPDATE operations SET status='pending'")
            state["target"]["file"]["name"] = "intervening.mkv"
            with self.assertRaisesRegex(RuntimeError, "object name changed"):
                MODULE.command_apply(args)
            state["target"]["file"]["name"] = "new.mkv"
            with plan:
                plan.execute("UPDATE operations SET status='pending',error=''")
            MODULE.command_apply(args)
            self.assertEqual(calls, {"mkdir": 1, "move": 1, "rename": 1})
            self.assertEqual(plan.execute("SELECT status FROM operations").fetchone()[0], "completed")
            plan.close()
        finally:
            (MODULE.load_cookie_and_assert_maintenance, MODULE.list_directory, MODULE.get_object, MODULE.locate_object, MODULE.mkdir, MODULE.move, MODULE.rename) = originals


if __name__ == "__main__":
    unittest.main()
