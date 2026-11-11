package file

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUserLocalStorageLifecycleAndPlacement(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	for _, uid := range []string{"369", "568"} {
		require.NoError(t, os.Mkdir(filepath.Join(base, "user-"+uid), 0o755))
	}
	svc, err := NewUserLocalFileService(base, "", "", func(_ context.Context, tenant uint64) (string, error) {
		if tenant == 7 {
			return "369", nil
		}
		if tenant == 8 {
			return "568", nil
		}
		return "", fmt.Errorf("no placement")
	})
	require.NoError(t, err)
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	p, err := w.CreateFormFile("file", "example.txt")
	require.NoError(t, err)
	_, err = p.Write([]byte("original knowledge"))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	req := httptest.NewRequest("POST", "/", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	require.NoError(t, req.ParseMultipartForm(1<<20))
	defer req.MultipartForm.RemoveAll()
	path, err := svc.SaveFile(ctx, req.MultipartForm.File["file"][0], 7, "document-a")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(path, "local://7/document-a/"))
	workspace := filepath.Join(base, "user-369", "gcs-webnora", "spaces", "7", "files")
	physical := filepath.Join(workspace, strings.TrimPrefix(path, "local://7/"))
	require.FileExists(t, physical)
	require.Equal(t, []byte("original knowledge"), readLocal(t, svc, path))
	copyPath, err := svc.CopyFile(ctx, path, 8, "copied-document")
	require.NoError(t, err)
	copyRoot := filepath.Join(base, "user-568/gcs-webnora/spaces/8/files")
	require.FileExists(t, filepath.Join(copyRoot, strings.TrimPrefix(copyPath, "local://8/")))
	require.NoError(t, svc.DeleteFile(ctx, path))
	require.NoFileExists(t, physical)
	require.Equal(t, []byte("original knowledge"), readLocal(t, svc, copyPath))
	export, err := svc.SaveBytes(ctx, []byte{0, 255, 42}, 7, "report.bin", false)
	require.NoError(t, err)
	require.Equal(t, []byte{0, 255, 42}, readLocal(t, svc, "storage://backend/"+export))
	_, err = svc.SaveBytes(ctx, []byte("x"), 99, "file.txt", false)
	require.Error(t, err)
}

func TestUserLocalStorageRejectsEscapeAndUnknownPlacement(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(base, "user-369"), 0o755))
	owner := func(context.Context, uint64) (string, error) { return "369", nil }
	svc, err := NewUserLocalFileService(base, "", "", owner)
	require.NoError(t, err)
	path, err := svc.SaveBytes(ctx, []byte("safe"), 7, "safe.txt", false)
	require.NoError(t, err)
	for _, bad := range []string{
		"local://7/../../user-568/dsh-home/config", "local://7/exports/../secret", "local://7//secret",
		"local://user-568/dsh-home/a", "/etc/passwd", "local://7/exports\\secret", "local://0/exports/a",
	} {
		_, err := svc.GetFile(ctx, bad)
		require.Error(t, err, bad)
		require.Error(t, svc.DeleteFile(ctx, bad), bad)
	}
	// An attacker in their Harness mount must not read another user's data by
	// replacing a knowledge file or a namespace boundary with a symlink.
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret"), []byte("private"), 0o600))
	root := filepath.Join(base, "user-369/gcs-webnora/spaces/7/files")
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err = svc.GetFile(ctx, "local://7/escape/secret")
	require.Error(t, err)
	require.Error(t, svc.DeleteFile(ctx, "local://7/escape/secret"))
	require.Equal(t, []byte("safe"), readLocal(t, svc, path))
	require.NoError(t, os.Symlink(outside, filepath.Join(base, "user-369/gcs-webnora/spaces/8")))
	_, err = svc.SaveBytes(ctx, []byte("no"), 8, "file.txt", false)
	require.Error(t, err)
}

func TestUserLocalFactoryRequiresAuthoritativePlacement(t *testing.T) {
	t.Setenv("LOCAL_STORAGE_LAYOUT", "platform-user")
	_, _, err := NewFileServiceFromStorageConfig("local", nil, t.TempDir())
	require.Error(t, err)
	owner := func(context.Context, uint64) (string, error) { return "369", nil }
	_, err = NewUserLocalFileService(t.TempDir(), "../../dsh-home", "", owner)
	require.Error(t, err)
}
