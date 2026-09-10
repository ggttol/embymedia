import importlib.util
import base64
import json
import pathlib
import sqlite3
import tempfile
import unittest

SCRIPT = pathlib.Path(__file__).with_name("cutover-emby-libraries.py")
SPEC = importlib.util.spec_from_file_location("cutover_emby_libraries", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(MODULE)


class EmbyLibraryCutoverTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.root = pathlib.Path(self.temporary.name)
        self.state = self.root / "state.json"
        self.plan = self.root / "plan.sqlite"
        self.layout = self.root / "layout.json"
        self.snapshot = self.root / "emby.sqlite"

    def tearDown(self):
        self.temporary.cleanup()

    def test_verify_accepts_each_planned_movie_version(self):
        database = sqlite3.connect(self.plan)
        database.execute("CREATE TABLE operations(target_directory_key TEXT,identity TEXT,target_name TEXT,disposition TEXT,status TEXT)")
        database.execute("INSERT INTO operations VALUES('title:电影:movie:1','movie:1','电影 (2025).mkv','canonical','completed')")
        database.execute("INSERT INTO operations VALUES('title:电影:movie:1','movie:1','电影 (2025) - Alternate.mkv','canonical','completed')")
        database.commit()
        database.close()
        self.state.write_text(json.dumps({"prepared": True, "new_libraries": {
            "电影": {"item_id": "library", "collection_type": "movies"}
        }}))
        originals = MODULE.load_connection, MODULE.paged
        MODULE.load_connection = lambda: ("base", "token")
        MODULE.paged = lambda *_args, **_kwargs: iter([
            {"Id": "movie-a", "Type": "Movie", "Name": "电影", "ProviderIds": {"Tmdb": "1"}, "Path": "/strm-v2/电影/电影-a.strm"},
            {"Id": "movie-b", "Type": "Movie", "Name": "电影", "ProviderIds": {"Tmdb": "1"}, "Path": "/strm-v2/电影/电影-b.strm"},
        ])
        try:
            MODULE.command_verify(type("Args", (), {"state": str(self.state), "plan": str(self.plan)})())
        finally:
            MODULE.load_connection, MODULE.paged = originals
        stored = json.loads(self.state.read_text())
        self.assertTrue(stored["verified"])
        self.assertEqual(stored["verification"]["电影"], {"items": 2, "identities": 1, "episodes": 0})

    def test_prepare_uploads_base64_encoded_poster(self):
        poster = self.root / "poster.jpg"
        poster.write_bytes(b"\xff\xd8poster\xff\xd9")
        self.layout.write_text(json.dumps({"libraries": [{"name": "电影", "collection_type": "movies", "poster": poster.name}]}))
        live = [
            {"ItemId": "old-movie", "Name": "old-movie", "CollectionType": "movies", "LibraryOptions": {}},
            {"ItemId": "old-series", "Name": "old-series", "CollectionType": "tvshows", "LibraryOptions": {}},
        ]
        uploads = []

        def post(_base, _token, path, body=None, content_type="application/json", params=None):
            if path == "/Library/VirtualFolders":
                live.append({"ItemId": "new", "Guid": "new-guid", "Name": "整理中·电影", "CollectionType": "movies", "Locations": ["/strm-v2/电影"]})
            elif path.endswith("/Images/Primary"):
                uploads.append((body, content_type))

        originals = MODULE.load_connection, MODULE.libraries, MODULE.get, MODULE.post, MODULE.wait_for_scan
        MODULE.load_connection = lambda: ("base", "token")
        MODULE.libraries = lambda *_args: live
        MODULE.get = lambda *_args: []
        MODULE.post = post
        MODULE.wait_for_scan = lambda *_args: {"result": {"Status": "Completed"}}
        try:
            MODULE.command_prepare(type("Args", (), {"state": str(self.state), "layout": str(self.layout), "posters": str(self.root)})())
        finally:
            MODULE.load_connection, MODULE.libraries, MODULE.get, MODULE.post, MODULE.wait_for_scan = originals
        self.assertEqual(uploads, [(base64.b64encode(poster.read_bytes()), "image/jpeg")])

    def test_migrate_state_maps_each_part_of_multi_episode_item(self):
        self.layout.write_text(json.dumps({"identity_overrides": {}}))
        self.state.write_text(json.dumps({"verified": True, "new_libraries": {
            "电视剧": {"item_id": "library", "collection_type": "tvshows"}
        }}))
        database = sqlite3.connect(self.snapshot)
        database.executescript("""
            CREATE TABLE items(id TEXT PRIMARY KEY,type TEXT,series_id TEXT,index_number INTEGER,parent_index_number INTEGER,provider_ids_json TEXT);
            CREATE TABLE user_state(user_id TEXT,item_id TEXT,played INTEGER,position_ticks INTEGER,is_favorite INTEGER,play_count INTEGER);
            INSERT INTO items VALUES('series','Series','',NULL,NULL,'{"Tmdb":"2"}');
            INSERT INTO user_state VALUES('user','series',1,0,0,0);
            INSERT INTO items VALUES('episode','Episode','series',3,1,'{}');
            INSERT INTO user_state VALUES('user','episode',0,123,1,4);
            INSERT INTO items VALUES('episode-copy','Episode','series',3,1,'{}');
            INSERT INTO user_state VALUES('user','episode-copy',1,50,0,2);
            INSERT INTO items VALUES('episode-four','Episode','series',4,1,'{}');
            INSERT INTO user_state VALUES('user','episode-four',0,456,0,1);
        """)
        database.close()
        posted = []
        new = [
            {"Id": "new-series", "Type": "Series", "ProviderIds": {"Tmdb": "2"}},
            {"Id": "new-episode", "Type": "Episode", "SeriesId": "new-series", "ParentIndexNumber": 1, "IndexNumber": 3, "IndexNumberEnd": 4, "ProviderIds": {}},
            {"Id": "new-episode-alternate", "Type": "Episode", "SeriesId": "new-series", "ParentIndexNumber": 1, "IndexNumber": 3, "IndexNumberEnd": 4, "ProviderIds": {}},
        ]
        originals = MODULE.load_connection, MODULE.paged, MODULE.post
        MODULE.load_connection = lambda: ("base", "token")
        MODULE.paged = lambda *_args, **_kwargs: iter(new)
        MODULE.post = lambda _base, _token, path, body=None, **_kwargs: posted.append((path, body))
        try:
            MODULE.command_migrate_state(type("Args", (), {"state": str(self.state), "layout": str(self.layout), "emby_snapshot": str(self.snapshot)})())
        finally:
            MODULE.load_connection, MODULE.paged, MODULE.post = originals
        self.assertEqual(posted, [
            ("/Users/user/Items/new-episode/UserData", {"Played": True, "PlaybackPositionTicks": 456, "IsFavorite": True, "PlayCount": 4}),
            ("/Users/user/Items/new-episode-alternate/UserData", {"Played": True, "PlaybackPositionTicks": 456, "IsFavorite": True, "PlayCount": 4}),
            ("/Users/user/Items/new-series/UserData", {"Played": True, "PlaybackPositionTicks": 0, "IsFavorite": False, "PlayCount": 0}),
        ])
        stored = json.loads(self.state.read_text())
        self.assertEqual((stored["migrated_user_state_rows"], stored["merged_user_state_rows"]), (3, 6))

    def test_cutover_does_not_delete_when_replacement_disappears(self):
        self.layout.write_text(json.dumps({"library_order": ["电影"], "system_library_posters": {}}))
        self.state.write_text(json.dumps({
            "verified": True,
            "user_state_migrated": True,
            "new_libraries": {"电影": {"item_id": "new", "collection_type": "movies", "location": "/strm-v2/电影", "temporary_name": "整理中·电影"}},
            "old_libraries": [{"ItemId": "old", "Name": "电影", "CollectionType": "movies"}],
        }))
        posted = []
        originals = MODULE.command_verify, MODULE.load_connection, MODULE.libraries, MODULE.post
        MODULE.command_verify = lambda _args: None
        MODULE.load_connection = lambda: ("base", "token")
        MODULE.libraries = lambda *_args: [{"ItemId": "old", "Name": "电影", "CollectionType": "movies", "Locations": ["/strm/电影"], "Guid": "old-guid"}]
        MODULE.post = lambda *args, **kwargs: posted.append((args, kwargs))
        try:
            args = type("Args", (), {"state": str(self.state), "layout": str(self.layout), "plan": str(self.plan)})()
            with self.assertRaisesRegex(RuntimeError, "replacement library changed"):
                MODULE.command_cutover(args)
        finally:
            MODULE.command_verify, MODULE.load_connection, MODULE.libraries, MODULE.post = originals
        self.assertEqual(posted, [])

    def test_explicit_user_state_identity_overrides_missing_episode_numbers(self):
        item = {"id": "disc", "type": "Episode", "series_id": "series", "index_number": None, "parent_index_number": None, "provider_ids_json": "{}"}
        self.assertEqual(MODULE.stable_key(item, {}, {}, {"disc": "episode:3:1:1"}), "episode:3:1:1")


if __name__ == "__main__":
    unittest.main()
