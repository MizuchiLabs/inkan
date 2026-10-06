package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestBackup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := Backup(t.Context(), dir, "")
	require.ErrorContains(t, err, "no database", "nothing to copy must not leave an empty database behind")
	require.NoFileExists(t, filepath.Join(dir, "inkan.db"))

	// The database stays open, a backup has to work while inkan runs.
	q, err := Open(t.Context(), dir)
	require.NoError(t, err)
	_, err = q.CreateProduct(t.Context(), &CreateProductParams{
		Name: "App", Prefix: "APP", Plan: "pro", PrivateKey: "the signing key",
	})
	require.NoError(t, err)

	out, err := Backup(t.Context(), dir, "")
	require.NoError(t, err)
	assert.Equal(t, dir, filepath.Dir(out), "without a path the copy lands next to the database")
	if runtime.GOOS != "windows" {
		info, err := os.Stat(out)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "the copy holds the signing keys")
	}

	copied, err := sql.Open("sqlite", "file:"+filepath.ToSlash(out))
	require.NoError(t, err)
	t.Cleanup(func() { _ = copied.Close() })
	p, err := New(copied).GetProductByPrefix(t.Context(), "APP")
	require.NoError(t, err)
	assert.Equal(t, "the signing key", p.PrivateKey)

	named := filepath.Join(t.TempDir(), "copy.db")
	got, err := Backup(t.Context(), dir, named)
	require.NoError(t, err)
	assert.Equal(t, named, got)
	_, err = Backup(t.Context(), dir, named)
	require.Error(t, err, "a backup must not replace a file that is there")
}

// The data directory of a container is often there before inkan, readable by
// others. The file with the signing keys must not be.
func TestOpenMakesTheDatabaseOwnerOnly(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("no file modes to speak of")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "inkan.db")
	require.NoError(t, os.WriteFile(path, nil, 0o644))

	_, err := Open(t.Context(), dir)
	require.NoError(t, err)
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
