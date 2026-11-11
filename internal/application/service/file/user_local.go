package file

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// StorageUserResolver resolves a durable workspace placement, never a request's
// current user. Sharing changes authorization, not the physical owner directory.
type StorageUserResolver func(context.Context, uint64) (string, error)

// PlatformUserStorageEnabled selects the Harness per-user filesystem layout.
func PlatformUserStorageEnabled() bool {
	return os.Getenv("LOCAL_STORAGE_LAYOUT") == "platform-user"
}

type userLocalFileService struct {
	*localFileService
	owner      StorageUserResolver
	prefix     string
	legacyBase string
}

// NewUserLocalFileService retains local://<tenant>/<object> handles while placing
// bytes under user-<platform UID>/gcs-webnora/spaces/<tenant>/files. Every actual
// filesystem operation is rooted in that workspace, including symlink resolution.
func NewUserLocalFileService(
	base, prefix, externalURL string, owner StorageUserResolver,
) (interfaces.FileService, error) {
	if owner == nil || !filepath.IsAbs(base) {
		return nil, fmt.Errorf("platform-user storage requires an absolute root and a storage owner resolver")
	}
	if prefix != "" && (!filepath.IsLocal(prefix) || strings.Contains(prefix, "\\")) {
		return nil, fmt.Errorf("invalid local storage instance prefix")
	}
	return &userLocalFileService{
		localFileService: &localFileService{
			baseDir: filepath.Clean(base), externalURL: strings.TrimRight(externalURL, "/"),
		},
		owner: owner, prefix: prefix, legacyBase: os.Getenv("LOCAL_STORAGE_LEGACY_BASE_DIR"),
	}, nil
}

func (s *userLocalFileService) CheckConnectivity(_ context.Context) error {
	r, err := os.OpenRoot(s.baseDir)
	if err != nil {
		return err
	}
	return r.Close()
}

func numericStorageID(value string) bool {
	n, err := strconv.ParseUint(value, 10, 64)
	return err == nil && n > 0 && strconv.FormatUint(n, 10) == value
}

// directory rejects symlinks at namespace boundaries; Root also prevents races
// in descendant object paths from escaping an already opened directory.
func directory(parent *os.Root, name string, create bool, mode os.FileMode) (*os.Root, error) {
	info, err := parent.Lstat(name)
	if os.IsNotExist(err) && create {
		if err = parent.Mkdir(name, mode); err != nil && !os.IsExist(err) {
			return nil, err
		}
		info, err = parent.Lstat(name)
	}
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("storage directory must not be a symlink: %s", name)
	}
	return parent.OpenRoot(name)
}

func (s *userLocalFileService) workspace(ctx context.Context, tenant uint64, create bool) (*os.Root, error) {
	uid, err := s.owner(ctx, tenant)
	if err != nil {
		return nil, err
	}
	if !numericStorageID(uid) || tenant == 0 {
		return nil, fmt.Errorf("invalid platform storage owner")
	}
	r, err := os.OpenRoot(s.baseDir)
	if err != nil {
		return nil, err
	}
	parts := []string{"user-" + uid, "gcs-webnora", "spaces", strconv.FormatUint(tenant, 10), "files"}
	if s.prefix != "" {
		parts = append(parts, strings.Split(filepath.ToSlash(filepath.Clean(s.prefix)), "/")...)
	}
	for i, part := range parts {
		mode := os.FileMode(0o700)
		if i == 0 {
			mode = 0o755
		}
		// The Worker provisions the user root as the Harness owner's directory.
		// A restricted knowledge service must never create it under its own UID,
		// which would prevent Harness from creating new top-level workspaces.
		next, openErr := directory(r, part, create && i > 0, mode)
		_ = r.Close()
		if openErr != nil {
			return nil, openErr
		}
		r = next
	}
	return r, nil
}

func (s *userLocalFileService) logical(filePath string) (uint64, string, error) {
	p := trimStorageScope(strings.TrimSpace(filePath))
	if strings.HasPrefix(p, "local:") {
		p = strings.TrimLeft(strings.TrimPrefix(p, "local:"), "/")
	} else if filepath.IsAbs(p) && s.legacyBase != "" {
		rel, err := filepath.Rel(s.legacyBase, p)
		if err != nil || !filepath.IsLocal(rel) {
			return 0, "", fmt.Errorf("file path outside legacy storage namespace")
		}
		p = filepath.ToSlash(rel)
	}
	if strings.ContainsAny(p, "\\\x00") {
		return 0, "", fmt.Errorf("invalid local storage path")
	}
	parts := strings.Split(p, "/")
	if len(parts) < 2 || !numericStorageID(parts[0]) {
		return 0, "", fmt.Errorf("local storage path requires its owning workspace")
	}
	for _, part := range parts[1:] {
		if part == "" || part == "." || part == ".." || strings.Contains(part, ":") {
			return 0, "", fmt.Errorf("invalid storage object path")
		}
	}
	tenant, _ := strconv.ParseUint(parts[0], 10, 64)
	return tenant, strings.Join(parts[1:], "/"), nil
}

func (s *userLocalFileService) save(
	ctx context.Context, src io.Reader, tenant uint64, dir, name string,
) (string, error) {
	if _, err := secutils.SafeFileName(dir); err != nil || dir == "." || dir == ".." {
		return "", fmt.Errorf("invalid knowledge directory")
	}
	r, err := s.workspace(ctx, tenant, true)
	if err != nil {
		return "", err
	}
	defer func() { _ = r.Close() }()
	d, err := directory(r, dir, true, 0o700)
	if err != nil {
		return "", err
	}
	defer func() { _ = d.Close() }()
	f, err := d.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	_, writeErr := io.Copy(f, src)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		_ = d.Remove(name)
		if writeErr != nil {
			return "", writeErr
		}
		return "", closeErr
	}
	return fmt.Sprintf("local://%d/%s/%s", tenant, dir, name), nil
}

func (s *userLocalFileService) SaveFile(
	ctx context.Context, header *multipart.FileHeader, tenant uint64, knowledge string,
) (string, error) {
	name, err := secutils.SafeFileName(header.Filename)
	if err != nil {
		return "", err
	}
	f, err := header.Open()
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	return s.save(ctx, f, tenant, knowledge, fmt.Sprintf("%d%s", time.Now().UnixNano(), filepath.Ext(name)))
}

func (s *userLocalFileService) SaveBytes(
	ctx context.Context, data []byte, tenant uint64, name string, _ bool,
) (string, error) {
	name, err := secutils.SafeFileName(name)
	if err != nil {
		return "", err
	}
	ext := filepath.Ext(name)
	filename := fmt.Sprintf("%s_%d%s", strings.TrimSuffix(name, ext), time.Now().UnixNano(), ext)
	return s.save(ctx, bytes.NewReader(data), tenant, "exports", filename)
}

func (s *userLocalFileService) GetFile(ctx context.Context, path string) (io.ReadCloser, error) {
	tenant, name, err := s.logical(path)
	if err != nil {
		return nil, err
	}
	r, err := s.workspace(ctx, tenant, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	return r.Open(name)
}

func (s *userLocalFileService) DeleteFile(ctx context.Context, path string) error {
	tenant, name, err := s.logical(path)
	if err != nil {
		return err
	}
	r, err := s.workspace(ctx, tenant, false)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	return r.Remove(name)
}

func (s *userLocalFileService) CopyFile(
	ctx context.Context, path string, tenant uint64, knowledge string,
) (string, error) {
	f, err := s.GetFile(ctx, path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	return s.save(ctx, f, tenant, knowledge, fmt.Sprintf("%d%s", time.Now().UnixNano(), filepath.Ext(path)))
}

func (s *userLocalFileService) GetFileURL(ctx context.Context, path string) (string, error) {
	tenant, name, err := s.logical(path)
	if err != nil {
		return "", err
	}
	return s.localFileService.GetFileURL(ctx, fmt.Sprintf("local://%d/%s", tenant, name))
}
