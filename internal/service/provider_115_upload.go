package service

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	sdk "github.com/OpenListTeam/115-sdk-go"
	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	"github.com/embymedia/embymedia/internal/domain"
)

const uploadPartSize int64 = 16 << 20

type UploadSource struct {
	Path      string
	Name      string
	Size      int64
	SHA1      string
	PreSHA1   string
	UploadID  string
	Bucket    string
	Object    string
	OnSession func(uploadID, bucket, object string) error
	OnPart    func(partNumber int, etag string, size int64) error
}

type UploadResult struct {
	FileID      string `json:"file_id"`
	Rapid       bool   `json:"rapid"`
	Transferred int64  `json:"transferred"`
}

type c115Credential struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

func c115SDK(account *domain.DriveAccount, client *http.Client) (*sdk.Client, error) {
	credential := c115Credential{}
	raw := strings.TrimSpace(account.Token)
	if raw == "" {
		return nil, fmt.Errorf("115 Open Platform access token is required for verified upload")
	}
	if strings.HasPrefix(raw, "{") {
		if err := json.Unmarshal([]byte(raw), &credential); err != nil {
			return nil, fmt.Errorf("invalid 115 upload credential")
		}
	} else {
		credential.AccessToken = raw
	}
	if strings.TrimSpace(credential.AccessToken) == "" {
		return nil, fmt.Errorf("115 Open Platform access token is required for verified upload")
	}
	options := []sdk.Option{sdk.WithAccessToken(credential.AccessToken)}
	if credential.RefreshToken != "" {
		options = append(options, sdk.WithRefreshToken(credential.RefreshToken))
	}
	return sdk.New(options...).SetHttpClient(client).SetUserAgent("EmbyMedia/2"), nil
}

func validSHA1(value string) bool {
	if len(value) != 40 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func hashFileRange(path, specification string) (string, error) {
	pieces := strings.Split(specification, "-")
	if len(pieces) != 2 {
		return "", fmt.Errorf("115 upload requested an invalid sign range")
	}
	start, err := strconv.ParseInt(pieces[0], 10, 64)
	if err != nil || start < 0 {
		return "", fmt.Errorf("115 upload requested an invalid sign range")
	}
	end, err := strconv.ParseInt(pieces[1], 10, 64)
	if err != nil || end < start {
		return "", fmt.Errorf("115 upload requested an invalid sign range")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return "", err
	}
	hash := sha1.New()
	if _, err := io.CopyN(hash, file, end-start+1); err != nil {
		return "", err
	}
	return strings.ToUpper(hex.EncodeToString(hash.Sum(nil))), nil
}

func callbackValues(init *sdk.UploadInitResp) (string, string) {
	if init.Callback.Value != nil {
		return init.Callback.Value.Callback, init.Callback.Value.CallbackVar
	}
	if len(init.Callback.Array) > 0 {
		return init.Callback.Array[0].Callback, init.Callback.Array[0].CallbackVar
	}
	return "", ""
}

func (p *Provider115) findDestinationFile(ctx context.Context, account *domain.DriveAccount, parent, name string) (*domain.DriveFile, error) {
	for offset := 0; offset < quarkMaxEntries; offset += 1000 {
		files, total, err := p.service.listFiles(ctx, account.ID, parent, offset, 1000)
		if err != nil {
			return nil, err
		}
		for i := range files {
			if files[i].Name == name {
				return &files[i], nil
			}
		}
		if len(files) == 0 || int64(offset+len(files)) >= total {
			return nil, nil
		}
	}
	return nil, fmt.Errorf("115 destination contains more than %d objects", quarkMaxEntries)
}

func verifyDestinationIdentity(file *domain.DriveFile, source UploadSource, parent string) error {
	if file == nil {
		return fmt.Errorf("115 upload completed but the destination object was not found")
	}
	if file.IsFolder || file.ParentID != parent || file.Name != source.Name || file.Size != source.Size || !strings.EqualFold(file.Sha1, source.SHA1) {
		return fmt.Errorf("115 destination identity verification failed for %s", source.Name)
	}
	return nil
}

// UploadFile rapidly uploads or multipart-uploads one already-spooled file and then
// verifies its exact parent, name, size and SHA-1 in 115 before returning.
func (p *Provider115) UploadFile(ctx context.Context, account *domain.DriveAccount, parentCID string, source UploadSource) (UploadResult, error) {
	if parentCID == "" {
		parentCID = "0"
	}
	source.Name = strings.TrimSpace(source.Name)
	source.SHA1 = strings.ToUpper(strings.TrimSpace(source.SHA1))
	source.PreSHA1 = strings.ToUpper(strings.TrimSpace(source.PreSHA1))
	if source.Path == "" || source.Name == "" || strings.ContainsAny(source.Name, "/\x00") || source.Size < 0 || !validSHA1(source.SHA1) || !validSHA1(source.PreSHA1) {
		return UploadResult{}, fmt.Errorf("validated spool path, name, size, SHA-1 and pre-SHA-1 are required")
	}
	stat, err := os.Stat(source.Path)
	if err != nil {
		return UploadResult{}, err
	}
	if !stat.Mode().IsRegular() || stat.Size() != source.Size {
		return UploadResult{}, fmt.Errorf("spool file size changed before upload")
	}
	existing, err := p.findDestinationFile(ctx, account, parentCID, source.Name)
	if err != nil {
		return UploadResult{}, err
	}
	if existing != nil {
		if err := verifyDestinationIdentity(existing, source, parentCID); err != nil {
			return UploadResult{}, fmt.Errorf("same-name destination conflict: %w", err)
		}
		return UploadResult{FileID: existing.FileID, Rapid: true}, nil
	}
	client, err := c115SDK(account, p.service.client)
	if err != nil {
		return UploadResult{}, err
	}
	init, err := client.UploadInit(ctx, &sdk.UploadInitReq{FileName: source.Name, FileSize: source.Size, Target: parentCID, FileID: source.SHA1, PreID: source.PreSHA1, TopUpload: "1"})
	if err != nil {
		return UploadResult{}, fmt.Errorf("initialize 115 upload: %w", err)
	}
	if init.Status == 7 && init.SignCheck != "" {
		signValue, err := hashFileRange(source.Path, init.SignCheck)
		if err != nil {
			return UploadResult{}, err
		}
		init, err = client.UploadInit(ctx, &sdk.UploadInitReq{FileName: source.Name, FileSize: source.Size, Target: parentCID, FileID: source.SHA1, PreID: source.PreSHA1, TopUpload: "1", SignKey: init.SignKey, SignVal: signValue})
		if err != nil {
			return UploadResult{}, fmt.Errorf("complete 115 rapid-upload challenge: %w", err)
		}
	}
	if init.Status == 2 {
		return p.waitForVerifiedDestination(ctx, account, parentCID, source, true, 0)
	}
	if init.Bucket == "" || init.Object == "" {
		return UploadResult{}, fmt.Errorf("115 upload initialization returned neither rapid success nor multipart coordinates")
	}
	token, err := client.UploadGetToken(ctx)
	if err != nil {
		return UploadResult{}, fmt.Errorf("get 115 OSS credentials: %w", err)
	}
	endpoint := strings.TrimSpace(token.Endpoint)
	if !strings.Contains(endpoint, "://") {
		endpoint = "https://" + endpoint
	}
	ossClient, err := oss.New(endpoint, token.AccessKeyId, token.AccessKeySecret, oss.SecurityToken(token.SecurityToken), oss.HTTPClient(p.service.client))
	if err != nil {
		return UploadResult{}, fmt.Errorf("initialize 115 OSS client: %w", err)
	}
	uploadBucket, uploadObject := init.Bucket, init.Object
	if source.UploadID != "" {
		if source.Bucket == "" || source.Object == "" {
			return UploadResult{}, fmt.Errorf("115 multipart checkpoint is incomplete")
		}
		uploadBucket, uploadObject = source.Bucket, source.Object
	}
	bucket, err := ossClient.Bucket(uploadBucket)
	if err != nil {
		return UploadResult{}, err
	}
	multipart := oss.InitiateMultipartUploadResult{Bucket: uploadBucket, Key: uploadObject, UploadID: source.UploadID}
	if multipart.UploadID == "" {
		multipart, err = bucket.InitiateMultipartUpload(uploadObject, oss.WithContext(ctx))
		if err != nil {
			return UploadResult{}, fmt.Errorf("initiate 115 multipart upload: %w", err)
		}
		if source.OnSession != nil {
			if err := source.OnSession(multipart.UploadID, uploadBucket, uploadObject); err != nil {
				_ = bucket.AbortMultipartUpload(multipart, oss.WithContext(ctx))
				return UploadResult{}, err
			}
		}
	}
	listed, err := bucket.ListUploadedParts(multipart, oss.WithContext(ctx))
	if err != nil {
		return UploadResult{}, fmt.Errorf("list 115 multipart parts: %w", err)
	}
	parts := make(map[int]oss.UploadPart, len(listed.UploadedParts))
	for _, uploaded := range listed.UploadedParts {
		parts[uploaded.PartNumber] = oss.UploadPart{PartNumber: uploaded.PartNumber, ETag: uploaded.ETag}
		if source.OnPart != nil {
			_ = source.OnPart(uploaded.PartNumber, uploaded.ETag, int64(uploaded.Size))
		}
	}
	file, err := os.Open(source.Path)
	if err != nil {
		return UploadResult{}, err
	}
	defer file.Close()
	partCount := int((source.Size + uploadPartSize - 1) / uploadPartSize)
	for number := 1; number <= partCount; number++ {
		if err := ctx.Err(); err != nil {
			return UploadResult{}, err
		}
		start := int64(number-1) * uploadPartSize
		size := min(uploadPartSize, source.Size-start)
		if _, present := parts[number]; present {
			continue
		}
		section := io.NewSectionReader(file, start, size)
		part, err := bucket.UploadPart(multipart, section, size, number, oss.WithContext(ctx))
		if err != nil {
			return UploadResult{}, fmt.Errorf("upload 115 multipart part %d: %w", number, err)
		}
		parts[number] = part
		if source.OnPart != nil {
			if err := source.OnPart(number, part.ETag, size); err != nil {
				return UploadResult{}, err
			}
		}
	}
	ordered := make([]oss.UploadPart, 0, len(parts))
	for _, part := range parts {
		ordered = append(ordered, part)
	}
	sort.Sort(oss.UploadParts(ordered))
	callback, callbackVar := callbackValues(init)
	options := []oss.Option{oss.WithContext(ctx)}
	if callback != "" {
		options = append(options, oss.Callback(callback))
	}
	if callbackVar != "" {
		options = append(options, oss.CallbackVar(callbackVar))
	}
	if _, err := bucket.CompleteMultipartUpload(multipart, ordered, options...); err != nil {
		return UploadResult{}, fmt.Errorf("complete 115 multipart upload: %w", err)
	}
	return p.waitForVerifiedDestination(ctx, account, parentCID, source, false, source.Size)
}

func (p *Provider115) waitForVerifiedDestination(ctx context.Context, account *domain.DriveAccount, parent string, source UploadSource, rapid bool, transferred int64) (UploadResult, error) {
	for attempt := 0; attempt < 30; attempt++ {
		file, err := p.findDestinationFile(ctx, account, parent, source.Name)
		if err == nil && file != nil {
			if err := verifyDestinationIdentity(file, source, parent); err != nil {
				return UploadResult{}, err
			}
			return UploadResult{FileID: file.FileID, Rapid: rapid, Transferred: transferred}, nil
		}
		if err != nil && ctx.Err() == nil {
			return UploadResult{}, err
		}
		if err := sleepContext(ctx, time.Second); err != nil {
			return UploadResult{}, err
		}
	}
	return UploadResult{}, fmt.Errorf("115 destination verification timed out for %s", source.Name)
}

func (p *Provider115) AbortUpload(ctx context.Context, account *domain.DriveAccount, source UploadSource) error {
	if source.UploadID == "" || source.Bucket == "" || source.Object == "" {
		return fmt.Errorf("complete multipart coordinates are required")
	}
	client, err := c115SDK(account, p.service.client)
	if err != nil {
		return err
	}
	token, err := client.UploadGetToken(ctx)
	if err != nil {
		return err
	}
	endpoint := strings.TrimSpace(token.Endpoint)
	if !strings.Contains(endpoint, "://") {
		endpoint = "https://" + endpoint
	}
	ossClient, err := oss.New(endpoint, token.AccessKeyId, token.AccessKeySecret, oss.SecurityToken(token.SecurityToken), oss.HTTPClient(p.service.client))
	if err != nil {
		return err
	}
	bucket, err := ossClient.Bucket(source.Bucket)
	if err != nil {
		return err
	}
	return bucket.AbortMultipartUpload(oss.InitiateMultipartUploadResult{Bucket: source.Bucket, Key: source.Object, UploadID: source.UploadID}, oss.WithContext(ctx))
}
