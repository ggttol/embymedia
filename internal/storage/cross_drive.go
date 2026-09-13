package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
)

func (d *DB) CreateCrossDriveImport(state *domain.CrossDriveImport) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if state.TaskID == "" || state.QuarkAccountID == "" || state.QuarkTargetID == "" || state.C115AccountID == "" {
		return fmt.Errorf("cross-drive import accounts, target, and task are required")
	}
	if state.CreatedAt.IsZero() {
		state.CreatedAt = time.Now()
	}
	state.UpdatedAt = time.Now()
	if state.Phase == "" {
		state.Phase = "saving_share"
	}
	// A task can be observed more than once by restart/retry code. Its source
	// and destination binding is immutable once persisted.
	var existing domain.CrossDriveImport
	var selected, expected, libraryName, libraryID, libraryCID, seriesID, tmdbID, folder string
	var nullableLibraryName, nullableLibraryID, nullableLibraryCID, nullableSeriesID, nullableTMDBID, nullableFolder sql.NullString
	err := d.db.QueryRow(`SELECT quark_account_id, quark_target_id, c115_account_id,
		selected_source_ids, autofill_library_name, autofill_library_id, autofill_library_cid,
		autofill_series_id, autofill_tmdb_id, autofill_series_folder, expected_episodes
		FROM cross_drive_imports WHERE task_id = ?`, state.TaskID).Scan(
		&existing.QuarkAccountID, &existing.QuarkTargetID, &existing.C115AccountID,
		&selected, &nullableLibraryName, &nullableLibraryID, &nullableLibraryCID,
		&nullableSeriesID, &nullableTMDBID, &nullableFolder, &expected)
	libraryName, libraryID, libraryCID = nullableLibraryName.String, nullableLibraryID.String, nullableLibraryCID.String
	seriesID, tmdbID, folder = nullableSeriesID.String, nullableTMDBID.String, nullableFolder.String
	existing.AutofillLibraryName, existing.AutofillLibraryID, existing.AutofillLibraryCID = libraryName, libraryID, libraryCID
	existing.AutofillSeriesID, existing.AutofillTMDBID, existing.AutofillSeriesFolder = seriesID, tmdbID, folder
	if err == nil {
		_ = json.Unmarshal([]byte(selected), &existing.SelectedSourceIDs)
		_ = json.Unmarshal([]byte(expected), &existing.ExpectedEpisodes)
		if !crossDriveBindingEqual(&existing, state) {
			return fmt.Errorf("cross-drive import %s destination/source binding changed", state.TaskID)
		}
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	selectedJSON, err := json.Marshal(state.SelectedSourceIDs)
	if err != nil {
		return err
	}
	expectedJSON, err := json.Marshal(state.ExpectedEpisodes)
	if err != nil {
		return err
	}
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO cross_drive_imports
		(task_id, prior_task_id, phase, quark_account_id, quark_target_id, c115_account_id,
		 selected_source_ids, autofill_library_name, autofill_library_id, autofill_library_cid,
		 autofill_series_id, autofill_tmdb_id, autofill_series_folder, expected_episodes,
		 saved_root_ids, destination_cid, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '[]', ?, ?, ?)`,
		state.TaskID, state.PriorTaskID, state.Phase, state.QuarkAccountID, state.QuarkTargetID,
		state.C115AccountID, string(selectedJSON), state.AutofillLibraryName, state.AutofillLibraryID,
		state.AutofillLibraryCID, state.AutofillSeriesID, state.AutofillTMDBID, state.AutofillSeriesFolder,
		string(expectedJSON), state.DestinationCID, state.CreatedAt, state.UpdatedAt)
	if err != nil {
		return err
	}
	if state.PriorTaskID != "" {
		if _, err := tx.Exec(`
			INSERT INTO cross_drive_items (task_id, source_file_id, source_revision, relative_path, name, size, sha1, pre_sha1, state, spool_path, downloaded_bytes, destination_parent, destination_id, upload_id, upload_bucket, upload_object, error, updated_at)
			SELECT ?, source_file_id, source_revision, relative_path, name, size, sha1, pre_sha1,
				CASE WHEN state = 'verified' THEN 'verified' WHEN state = 'downloaded' AND downloaded_bytes = size THEN 'downloaded' ELSE 'discovered' END,
				spool_path, CASE WHEN downloaded_bytes = size THEN downloaded_bytes ELSE 0 END, destination_parent, destination_id,
				CASE WHEN state = 'uploading' THEN upload_id ELSE '' END, upload_bucket, upload_object, '', ?
			FROM cross_drive_items WHERE task_id = ?`, state.TaskID, time.Now(), state.PriorTaskID); err != nil {
			return err
		}
		if _, err := tx.Exec(`
			INSERT INTO cross_drive_upload_parts (item_id, part_number, etag, size)
			SELECT replacement.id, part.part_number, part.etag, part.size
			FROM cross_drive_upload_parts part
			JOIN cross_drive_items original ON original.id = part.item_id
			JOIN cross_drive_items replacement ON replacement.task_id = ? AND replacement.source_file_id = original.source_file_id
			WHERE original.task_id = ? AND replacement.upload_id != ''`, state.TaskID, state.PriorTaskID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func crossDriveBindingEqual(a, b *domain.CrossDriveImport) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.QuarkAccountID != b.QuarkAccountID || a.QuarkTargetID != b.QuarkTargetID || a.C115AccountID != b.C115AccountID ||
		a.AutofillLibraryName != b.AutofillLibraryName || a.AutofillLibraryID != b.AutofillLibraryID ||
		a.AutofillLibraryCID != b.AutofillLibraryCID || a.AutofillSeriesID != b.AutofillSeriesID ||
		a.AutofillTMDBID != b.AutofillTMDBID || a.AutofillSeriesFolder != b.AutofillSeriesFolder {
		return false
	}
	if strings.Join(a.SelectedSourceIDs, "\x00") != strings.Join(b.SelectedSourceIDs, "\x00") ||
		strings.Join(a.ExpectedEpisodes, "\x00") != strings.Join(b.ExpectedEpisodes, "\x00") {
		return false
	}
	return true
}

func scanCrossDriveImport(scanner rowScanner) (*domain.CrossDriveImport, error) {
	var state domain.CrossDriveImport
	var prior, selected, libraryName, libraryID, libraryCID, seriesID, tmdbID, folder, expected, roots, destination, current, errText sql.NullString
	var cancelled int
	err := scanner.Scan(&state.TaskID, &prior, &state.Phase, &state.QuarkAccountID, &state.QuarkTargetID,
		&state.C115AccountID, &selected, &libraryName, &libraryID, &libraryCID, &seriesID, &tmdbID, &folder,
		&expected, &roots, &destination, &state.TotalFiles, &state.CompletedFiles, &state.TotalBytes,
		&state.CompletedBytes, &current, &cancelled, &errText, &state.CreatedAt, &state.UpdatedAt)
	if err != nil {
		return nil, err
	}
	state.PriorTaskID = prior.String
	state.AutofillLibraryName, state.AutofillLibraryID, state.AutofillLibraryCID = libraryName.String, libraryID.String, libraryCID.String
	state.AutofillSeriesID, state.AutofillTMDBID, state.AutofillSeriesFolder = seriesID.String, tmdbID.String, folder.String
	if selected.String != "" {
		if err := json.Unmarshal([]byte(selected.String), &state.SelectedSourceIDs); err != nil {
			return nil, fmt.Errorf("decode selected source IDs: %w", err)
		}
	}
	if expected.String != "" {
		if err := json.Unmarshal([]byte(expected.String), &state.ExpectedEpisodes); err != nil {
			return nil, fmt.Errorf("decode expected episodes: %w", err)
		}
	}
	if roots.String != "" {
		if err := json.Unmarshal([]byte(roots.String), &state.SavedRootIDs); err != nil {
			return nil, fmt.Errorf("decode saved root IDs: %w", err)
		}
	}
	state.DestinationCID = destination.String
	state.CurrentFile = current.String
	state.CancelRequested = cancelled != 0
	state.Error = errText.String
	return &state, nil
}

func (d *DB) GetCrossDriveImport(taskID string) (*domain.CrossDriveImport, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return scanCrossDriveImport(d.db.QueryRow(`SELECT task_id, prior_task_id, phase, quark_account_id, quark_target_id, c115_account_id,
		selected_source_ids, autofill_library_name, autofill_library_id, autofill_library_cid, autofill_series_id, autofill_tmdb_id,
		autofill_series_folder, expected_episodes, saved_root_ids, destination_cid, total_files, completed_files, total_bytes,
		completed_bytes, current_file, cancel_requested, error, created_at, updated_at
		FROM cross_drive_imports WHERE task_id = ?`, taskID))
}

func scanCrossDriveItem(scanner rowScanner) (*domain.CrossDriveItem, error) {
	var item domain.CrossDriveItem
	var revision, sha, pre, spool, parent, destination, uploadID, bucket, object, errText sql.NullString
	err := scanner.Scan(&item.ID, &item.TaskID, &item.SourceFileID, &revision, &item.RelativePath, &item.Name, &item.Size, &sha, &pre, &item.State, &spool, &item.DownloadedBytes, &parent, &destination, &uploadID, &bucket, &object, &errText, &item.UpdatedAt)
	if err != nil {
		return nil, err
	}
	item.SourceRevision = revision.String
	item.SHA1 = sha.String
	item.PreSHA1 = pre.String
	item.SpoolPath = spool.String
	item.DestinationParent = parent.String
	item.DestinationID = destination.String
	item.UploadID = uploadID.String
	item.UploadBucket = bucket.String
	item.UploadObject = object.String
	item.Error = errText.String
	return &item, nil
}

func (d *DB) ListCrossDriveItems(taskID string) ([]domain.CrossDriveItem, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	rows, err := d.db.Query(`SELECT id, task_id, source_file_id, source_revision, relative_path, name, size, sha1, pre_sha1, state, spool_path, downloaded_bytes, destination_parent, destination_id, upload_id, upload_bucket, upload_object, error, updated_at FROM cross_drive_items WHERE task_id = ? ORDER BY relative_path`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.CrossDriveItem, 0)
	for rows.Next() {
		item, err := scanCrossDriveItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (d *DB) GetCrossDriveImportDetail(taskID string) (*domain.CrossDriveImportDetail, error) {
	state, err := d.GetCrossDriveImport(taskID)
	if err != nil {
		return nil, err
	}
	if state.AutofillLibraryName != "" && state.AutofillSeriesFolder != "" {
		state.DestinationPath = path.Join("/", state.AutofillLibraryName, state.AutofillSeriesFolder)
	} else if state.DestinationCID != "" {
		state.DestinationPath = "/emby/_待整理"
	}
	items, err := d.ListCrossDriveItems(taskID)
	if err != nil {
		return nil, err
	}
	return &domain.CrossDriveImportDetail{Import: *state, Items: items}, nil
}

func (d *DB) ClaimCrossDriveImport(taskID, owner string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	result, err := d.db.Exec(`UPDATE cross_drive_imports SET run_owner = ?, updated_at = ? WHERE task_id = ? AND (run_owner IS NULL OR run_owner = '')`, owner, time.Now(), taskID)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return fmt.Errorf("cross-drive import %s is owned by another run", taskID)
	}
	return nil
}
func (d *DB) ReleaseCrossDriveImport(taskID, owner string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec(`UPDATE cross_drive_imports SET run_owner = NULL, updated_at = ? WHERE task_id = ? AND run_owner = ?`, time.Now(), taskID, owner)
	return err
}
func (d *DB) UpdateCrossDriveImport(taskID, owner, phase, destination, current, errText string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if destination != "" {
		var persisted sql.NullString
		if err := d.db.QueryRow(`SELECT destination_cid FROM cross_drive_imports WHERE task_id = ?`, taskID).Scan(&persisted); err != nil && err != sql.ErrNoRows {
			return err
		} else if persisted.Valid && persisted.String != "" && persisted.String != destination {
			return fmt.Errorf("cross-drive import %s destination CID changed", taskID)
		}
	}
	result, err := d.db.Exec(`UPDATE cross_drive_imports SET phase = ?, destination_cid = CASE WHEN ? != '' THEN ? ELSE destination_cid END, current_file = ?, error = ?, updated_at = ? WHERE task_id = ? AND run_owner = ?`, phase, destination, destination, current, errText, time.Now(), taskID, owner)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return fmt.Errorf("cross-drive import %s lost run ownership", taskID)
	}
	return nil
}
func (d *DB) SaveCrossDriveRoots(taskID, owner string, roots []string) error {
	encoded, err := json.Marshal(roots)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	result, err := d.db.Exec(`UPDATE cross_drive_imports SET saved_root_ids = ?, phase = 'discovering', updated_at = ? WHERE task_id = ? AND run_owner = ?`, string(encoded), time.Now(), taskID, owner)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return fmt.Errorf("cross-drive import %s lost run ownership", taskID)
	}
	return nil
}
func (d *DB) RequestCrossDriveCancellation(taskID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec(`UPDATE cross_drive_imports SET cancel_requested = 1, updated_at = ? WHERE task_id = ?`, time.Now(), taskID)
	return err
}

func (d *DB) UpsertCrossDriveItem(taskID, owner string, item *domain.CrossDriveItem) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if item == nil || strings.TrimSpace(item.SourceFileID) == "" {
		return fmt.Errorf("cross-drive source file ID is required")
	}
	var revision, relative, name, sha sql.NullString
	var size int64
	err := d.db.QueryRow(`SELECT source_revision, relative_path, name, size, sha1 FROM cross_drive_items WHERE task_id = ? AND source_file_id = ?`, taskID, item.SourceFileID).Scan(&revision, &relative, &name, &size, &sha)
	if err == nil {
		if (revision.String != "" && item.SourceRevision != "" && revision.String != item.SourceRevision) ||
			relative.String != item.RelativePath || name.String != item.Name || size != item.Size ||
			(sha.String != "" && item.SHA1 != "" && !strings.EqualFold(sha.String, item.SHA1)) {
			return fmt.Errorf("cross-drive source identity changed for %s", item.SourceFileID)
		}
	} else if err != sql.ErrNoRows {
		return err
	}
	now := time.Now()
	result, err := d.db.Exec(`INSERT INTO cross_drive_items (task_id, source_file_id, source_revision, relative_path, name, size, sha1, state, updated_at)
		SELECT ?, ?, ?, ?, ?, ?, ?, 'discovered', ?
		WHERE EXISTS (SELECT 1 FROM cross_drive_imports WHERE task_id = ? AND run_owner = ?)
		ON CONFLICT(task_id, source_file_id) DO UPDATE SET
			updated_at = excluded.updated_at,
			sha1 = COALESCE(NULLIF(excluded.sha1, ''), cross_drive_items.sha1)`,
		taskID, item.SourceFileID, item.SourceRevision, item.RelativePath, item.Name, item.Size, item.SHA1, now, taskID, owner)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return fmt.Errorf("cross-drive import %s lost run ownership", taskID)
	}
	return nil
}

func (d *DB) FinalizeCrossDriveDiscovery(taskID, owner string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	result, err := d.db.Exec(`UPDATE cross_drive_imports SET total_files = (SELECT COUNT(*) FROM cross_drive_items WHERE task_id = ?), total_bytes = COALESCE((SELECT SUM(size) FROM cross_drive_items WHERE task_id = ?), 0), updated_at = ? WHERE task_id = ? AND run_owner = ?`, taskID, taskID, time.Now(), taskID, owner)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return fmt.Errorf("cross-drive import %s lost run ownership", taskID)
	}
	return nil
}

func (d *DB) UpdateCrossDriveItemDownload(itemID int64, taskID, owner, state, spool string, downloaded int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	result, err := d.db.Exec(`UPDATE cross_drive_items SET state = ?, spool_path = ?, downloaded_bytes = ?, error = '', updated_at = ? WHERE id = ? AND task_id = ? AND EXISTS (SELECT 1 FROM cross_drive_imports WHERE task_id = ? AND run_owner = ?)`, state, spool, downloaded, time.Now(), itemID, taskID, taskID, owner)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return fmt.Errorf("cross-drive item %d lost run ownership", itemID)
	}
	return nil
}
func (d *DB) UpdateCrossDriveItemHashes(itemID int64, taskID, owner, sha, pre string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec(`UPDATE cross_drive_items SET sha1 = ?, pre_sha1 = ?, state = 'downloaded', downloaded_bytes = size, updated_at = ? WHERE id = ? AND task_id = ? AND EXISTS (SELECT 1 FROM cross_drive_imports WHERE task_id = ? AND run_owner = ?)`, sha, pre, time.Now(), itemID, taskID, taskID, owner)
	return err
}
func (d *DB) ResetCrossDriveUpload(itemID int64, taskID, owner string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM cross_drive_upload_parts WHERE item_id = ?`, itemID); err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE cross_drive_items SET state = 'downloaded', upload_id = '', upload_bucket = '', upload_object = '', updated_at = ? WHERE id = ? AND task_id = ? AND EXISTS (SELECT 1 FROM cross_drive_imports WHERE task_id = ? AND run_owner = ?)`, time.Now(), itemID, taskID, taskID, owner)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (d *DB) SaveCrossDriveUploadSession(itemID int64, taskID, owner, uploadID, bucket, object string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec(`UPDATE cross_drive_items SET state = 'uploading', upload_id = ?, upload_bucket = ?, upload_object = ?, updated_at = ? WHERE id = ? AND task_id = ? AND EXISTS (SELECT 1 FROM cross_drive_imports WHERE task_id = ? AND run_owner = ?)`, uploadID, bucket, object, time.Now(), itemID, taskID, taskID, owner)
	return err
}
func (d *DB) SaveCrossDriveUploadPart(itemID int64, partNumber int, etag string, size int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec(`INSERT INTO cross_drive_upload_parts (item_id, part_number, etag, size) VALUES (?, ?, ?, ?) ON CONFLICT(item_id, part_number) DO UPDATE SET etag = excluded.etag, size = excluded.size`, itemID, partNumber, etag, size)
	return err
}
func (d *DB) MarkCrossDriveItemVerified(itemID int64, taskID, owner, parent, destination string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE cross_drive_items SET state = 'verified', destination_parent = ?, destination_id = ?, error = '', updated_at = ? WHERE id = ? AND task_id = ? AND EXISTS (SELECT 1 FROM cross_drive_imports WHERE task_id = ? AND run_owner = ?)`, parent, destination, time.Now(), itemID, taskID, taskID, owner)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return fmt.Errorf("cross-drive item %d lost run ownership", itemID)
	}
	if _, err := tx.Exec(`UPDATE cross_drive_imports SET completed_files = (SELECT COUNT(*) FROM cross_drive_items WHERE task_id = ? AND state = 'verified'), completed_bytes = COALESCE((SELECT SUM(size) FROM cross_drive_items WHERE task_id = ? AND state = 'verified'), 0), updated_at = ? WHERE task_id = ? AND run_owner = ?`, taskID, taskID, time.Now(), taskID, owner); err != nil {
		return err
	}
	return tx.Commit()
}
func (d *DB) MarkCrossDriveItemFailed(itemID int64, taskID, owner, message string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec(`UPDATE cross_drive_items SET state = 'failed', error = ?, updated_at = ? WHERE id = ? AND task_id = ? AND EXISTS (SELECT 1 FROM cross_drive_imports WHERE task_id = ? AND run_owner = ?)`, message, time.Now(), itemID, taskID, taskID, owner)
	return err
}
