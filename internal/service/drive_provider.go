package service

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/embymedia/embymedia/internal/domain"
)

// DriveProvider is the provider boundary used by provider-neutral REST and transfer workflows.
// Remote object IDs are opaque and must never be parsed or converted to numbers.
type DriveProvider interface {
	Name() string
	Capabilities() DriveCapabilities
	CheckAccount(context.Context, *domain.DriveAccount) error
	ListFiles(context.Context, *domain.DriveAccount, string, int, int) ([]domain.DriveFile, int64, error)
	SearchFiles(context.Context, *domain.DriveAccount, string, int, int) ([]domain.DriveFile, int64, error)
	Mkdir(context.Context, *domain.DriveAccount, string, string) (string, error)
	Rename(context.Context, *domain.DriveAccount, string, string) error
	Move(context.Context, *domain.DriveAccount, []string, string) error
	Delete(context.Context, *domain.DriveAccount, []string) error
	SnapshotShare(context.Context, *domain.DriveAccount, string, string) (ShareSnapshot, error)
	SaveShare(context.Context, *domain.DriveAccount, string, string, string) (SavedShare, error)
	OpenDownload(context.Context, *domain.DriveAccount, string, int64) (io.ReadCloser, error)
}

type DriveCapabilities struct {
	Browse       bool `json:"browse"`
	Search       bool `json:"search"`
	Mkdir        bool `json:"mkdir"`
	Rename       bool `json:"rename"`
	Move         bool `json:"move"`
	Delete       bool `json:"delete"`
	ShareSave    bool `json:"share_save"`
	Download     bool `json:"download"`
	Offline      bool `json:"offline"`
	GenerateLink bool `json:"generate_link"`
}

type ShareSnapshot struct {
	Title   string       `json:"title"`
	Entries []ShareEntry `json:"entries"`
}

type SavedShare struct {
	Title   string   `json:"title"`
	RootIDs []string `json:"root_ids"`
	Count   int      `json:"count"`
}

type Provider115 struct{ service *DriveService }

func (p *Provider115) Name() string { return "115" }
func (p *Provider115) Capabilities() DriveCapabilities {
	return DriveCapabilities{Browse: true, Search: true, Mkdir: true, Rename: true, Move: true, Delete: true, ShareSave: true, Download: true, Offline: true, GenerateLink: true}
}
func (p *Provider115) CheckAccount(ctx context.Context, account *domain.DriveAccount) error {
	return p.service.check115Account(ctx, account)
}
func (p *Provider115) ListFiles(ctx context.Context, account *domain.DriveAccount, parent string, offset, limit int) ([]domain.DriveFile, int64, error) {
	return p.service.listFiles(ctx, account.ID, parent, offset, limit)
}
func (p *Provider115) SearchFiles(ctx context.Context, account *domain.DriveAccount, query string, offset, limit int) ([]domain.DriveFile, int64, error) {
	return p.service.SearchFilesCtx(ctx, account.ID, query, offset, limit)
}
func (p *Provider115) Mkdir(ctx context.Context, account *domain.DriveAccount, parent, name string) (string, error) {
	return p.service.makeDir(ctx, account.ID, parent, name)
}
func (p *Provider115) Rename(ctx context.Context, account *domain.DriveAccount, id, name string) error {
	return p.service.rename(ctx, account.ID, id, name)
}
func (p *Provider115) Move(ctx context.Context, account *domain.DriveAccount, ids []string, parent string) error {
	return p.service.move(ctx, account.ID, ids, parent)
}
func (p *Provider115) Delete(ctx context.Context, account *domain.DriveAccount, ids []string) error {
	return p.service.DeleteCtx(ctx, account.ID, ids)
}
func (p *Provider115) SnapshotShare(ctx context.Context, account *domain.DriveAccount, rawURL, password string) (ShareSnapshot, error) {
	code, receive, err := ParseShareCode(rawURL, password)
	if err != nil {
		return ShareSnapshot{}, err
	}
	title, entries, err := p.service.snapshotShareEntries(ctx, account, code, receive, "0")
	return ShareSnapshot{Title: title, Entries: entries}, err
}
func (p *Provider115) SaveShare(ctx context.Context, account *domain.DriveAccount, rawURL, password, parent string) (SavedShare, error) {
	code, receive, err := ParseShareCode(rawURL, password)
	if err != nil {
		return SavedShare{}, err
	}
	title, entries, err := p.service.snapshotShareEntries(ctx, account, code, receive, "0")
	if err != nil {
		return SavedShare{}, err
	}
	if len(entries) == 0 {
		return SavedShare{}, fmt.Errorf("115 分享内容为空或已失效")
	}
	ids := make([]string, len(entries))
	for i := range entries {
		ids[i] = entries[i].ID
	}
	count, title, err := p.service.receiveShareEntriesCtx(ctx, account, code, receive, ids, parent, title)
	return SavedShare{Title: title, RootIDs: ids, Count: count}, err
}
func (p *Provider115) OpenDownload(context.Context, *domain.DriveAccount, string, int64) (io.ReadCloser, error) {
	return nil, fmt.Errorf("115 ranged download is not exposed by this adapter")
}

func normalizeProvider(provider string) (string, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "115" || provider == "quark" {
		return provider, nil
	}
	return "", fmt.Errorf("provider must be 115 or quark")
}

func (s *DriveService) provider(name string) (DriveProvider, error) {
	name, err := normalizeProvider(name)
	if err != nil {
		return nil, err
	}
	provider := s.providers[name]
	if provider == nil {
		return nil, fmt.Errorf("drive provider %s is unavailable", name)
	}
	return provider, nil
}

func (s *DriveService) getAccountForProvider(provider, id string) (*domain.DriveAccount, error) {
	provider, err := normalizeProvider(provider)
	if err != nil {
		return nil, err
	}
	if id == "" {
		return s.GetDefaultAccount(provider)
	}
	accounts, err := s.db.ListAccounts()
	if err != nil {
		return nil, err
	}
	for i := range accounts {
		if accounts[i].ID == id {
			if accounts[i].Type != provider {
				return nil, fmt.Errorf("account %s belongs to provider %s, not %s", id, accounts[i].Type, provider)
			}
			return &accounts[i], nil
		}
	}
	return nil, fmt.Errorf("%s account %s was not found", provider, id)
}

func (s *DriveService) GetAccount(provider, id string) (*domain.DriveAccount, error) {
	return s.getAccountForProvider(provider, id)
}

func (s *DriveService) ProviderCapabilities(provider string) (DriveCapabilities, error) {
	p, err := s.provider(provider)
	if err != nil {
		return DriveCapabilities{}, err
	}
	return p.Capabilities(), nil
}

func (s *DriveService) ListProviderFiles(ctx context.Context, provider, accountID, parent string, offset, limit int) ([]domain.DriveFile, int64, error) {
	account, err := s.getAccountForProvider(provider, accountID)
	if err != nil {
		return nil, 0, err
	}
	p, err := s.provider(provider)
	if err != nil {
		return nil, 0, err
	}
	return p.ListFiles(ctx, account, parent, offset, limit)
}
func (s *DriveService) SearchProviderFiles(ctx context.Context, provider, accountID, query string, offset, limit int) ([]domain.DriveFile, int64, error) {
	account, err := s.getAccountForProvider(provider, accountID)
	if err != nil {
		return nil, 0, err
	}
	p, err := s.provider(provider)
	if err != nil {
		return nil, 0, err
	}
	return p.SearchFiles(ctx, account, query, offset, limit)
}
func (s *DriveService) MkdirProvider(ctx context.Context, provider, accountID, parent, name string) (string, error) {
	account, err := s.getAccountForProvider(provider, accountID)
	if err != nil {
		return "", err
	}
	p, err := s.provider(provider)
	if err != nil {
		return "", err
	}
	return p.Mkdir(ctx, account, parent, name)
}
func (s *DriveService) RenameProvider(ctx context.Context, provider, accountID, id, name string) error {
	account, err := s.getAccountForProvider(provider, accountID)
	if err != nil {
		return err
	}
	p, err := s.provider(provider)
	if err != nil {
		return err
	}
	return p.Rename(ctx, account, id, name)
}
func (s *DriveService) MoveProvider(ctx context.Context, provider, accountID string, ids []string, parent string) error {
	account, err := s.getAccountForProvider(provider, accountID)
	if err != nil {
		return err
	}
	p, err := s.provider(provider)
	if err != nil {
		return err
	}
	return p.Move(ctx, account, ids, parent)
}
func (s *DriveService) DeleteProvider(ctx context.Context, provider, accountID string, ids []string) error {
	account, err := s.getAccountForProvider(provider, accountID)
	if err != nil {
		return err
	}
	p, err := s.provider(provider)
	if err != nil {
		return err
	}
	return p.Delete(ctx, account, ids)
}
func (s *DriveService) SnapshotProviderShare(ctx context.Context, provider, accountID, rawURL, password string) (ShareSnapshot, error) {
	account, err := s.getAccountForProvider(provider, accountID)
	if err != nil {
		return ShareSnapshot{}, err
	}
	p, err := s.provider(provider)
	if err != nil {
		return ShareSnapshot{}, err
	}
	return p.SnapshotShare(ctx, account, rawURL, password)
}
func (s *DriveService) SaveProviderShare(ctx context.Context, provider, accountID, rawURL, password, parent string) (SavedShare, error) {
	account, err := s.getAccountForProvider(provider, accountID)
	if err != nil {
		return SavedShare{}, err
	}
	p, err := s.provider(provider)
	if err != nil {
		return SavedShare{}, err
	}
	return p.SaveShare(ctx, account, rawURL, password, parent)
}

func (s *DriveService) OpenProviderDownload(ctx context.Context, provider, accountID, fileID string, offset int64) (io.ReadCloser, error) {
	account, err := s.getAccountForProvider(provider, accountID)
	if err != nil {
		return nil, err
	}
	p, err := s.provider(provider)
	if err != nil {
		return nil, err
	}
	return p.OpenDownload(ctx, account, fileID, offset)
}

func (s *DriveService) ResolveProviderDeleteTargets(ctx context.Context, provider, accountID, parent string, fileIDs []string) (string, []domain.DestructiveTarget, error) {
	if len(fileIDs) == 0 || len(fileIDs) > 100 {
		return "", nil, fmt.Errorf("file_ids must contain 1 to 100 values")
	}
	account, err := s.getAccountForProvider(provider, accountID)
	if err != nil {
		return "", nil, err
	}
	if parent == "" {
		parent = "0"
	}
	wanted := make(map[string]struct{}, len(fileIDs))
	for _, id := range fileIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			return "", nil, fmt.Errorf("file_ids must contain non-empty values")
		}
		if _, duplicate := wanted[id]; duplicate {
			return "", nil, fmt.Errorf("file_ids must not contain duplicates")
		}
		wanted[id] = struct{}{}
	}
	found := make(map[string]domain.DestructiveTarget, len(wanted))
	for offset := 0; offset < quarkMaxEntries && len(found) < len(wanted); offset += 200 {
		files, total, err := s.ListProviderFiles(ctx, provider, account.ID, parent, offset, 200)
		if err != nil {
			return "", nil, err
		}
		for _, file := range files {
			if _, selected := wanted[file.FileID]; selected {
				found[file.FileID] = domain.DestructiveTarget{FileID: file.FileID, Name: file.Name, IsFolder: file.IsFolder, Size: file.Size}
			}
		}
		if len(files) == 0 || int64(offset+len(files)) >= total {
			break
		}
	}
	targets := make([]domain.DestructiveTarget, 0, len(fileIDs))
	for _, id := range fileIDs {
		target, ok := found[strings.TrimSpace(id)]
		if !ok {
			return "", nil, fmt.Errorf("%s object %s was not found in parent %s", provider, id, parent)
		}
		targets = append(targets, target)
	}
	return account.ID, targets, nil
}
