package main

import (
	"archive/tar"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// backupCmd writes every CV to one file.
//
// # WHY THE SERVICE HAS THIS AT ALL
//
// The documentation said "tar the data directory", which is true and is not a
// backup: it is an instruction somebody has to remember, at an hour nobody is
// awake, with no way to find out whether what came out can be read back.
//
// This does the same thing and can be checked, restored and rotated — and it
// runs from a timer, which the instruction never did.
//
// # WHAT GOES IN, AND WHAT DELIBERATELY DOES NOT
//
// Everything under the data directory that is a CV: documents, portraits,
// journals, and the link file — WITHOUT which a restored service is a service
// nobody can open, since the links are the only credential there is.
//
// Not the trash: it holds what somebody asked to be rid of, and a backup that
// resurrects deleted CVs is a backup that undoes a deletion somebody meant.
//
// # WHY IT DOES NOT STOP THE SERVICE
//
// Every write is a temporary file renamed into place, so a reader sees the old
// document or the new one and never half of either. A backup taken while the
// service runs therefore catches a consistent version of every file — possibly
// from slightly different moments, which for a set of independent CVs is not a
// meaningful difference.
// configuredDataDir is where the CVs are, asked the same way the service asks.
//
// `backup` and `restore` used to work it out themselves, from the environment
// and a path beside the binary — so on a machine configured by
// /etc/picvert.yaml, which is every installed one, they looked in the wrong
// place and reported "no data directory". The service and its own backup
// command disagreed about where the data was, and the command that disagreed
// was the one you reach for when moving a machine.
//
// Through config.Load, so the file, the environment and the defaults are
// layered in the one order this project states everywhere else. A configuration
// that will not parse is not fatal here: a backup is the thing you want MOST
// when something is wrong with the configuration.
func configuredDataDir() string {
	if cfg, err := loadConfig(); err == nil {
		return cfg.ResolvedDataDir()
	}
	if root, err := home(); err == nil {
		return envOr("PICVERT_DATA", filepath.Join(root, "data"))
	}
	return envOr("PICVERT_DATA", "data")
}

func backupCmd(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	out := fs.String("out", "", "file to write (default: picvert-<date>.tar.gz here)")
	dir := fs.String("data", "", "the data directory (default: the configured one)")
	keep := fs.Int("keep", 0, "delete older backups beside this one, keeping N")
	if err := fs.Parse(args); err != nil {
		return err
	}

	data := *dir
	if data == "" {
		data = configuredDataDir()
	}
	if _, err := os.Stat(data); err != nil {
		return fmt.Errorf("no data directory at %s", data)
	}

	name := *out
	if name == "" {
		name = fmt.Sprintf("picvert-%s.tar.gz", time.Now().Format("2006-01-02-1504"))
	}

	// Written to a temporary name and renamed at the end, so a backup
	// interrupted half way through is not left looking like a finished one.
	// The moment that matters is a disk filling up during the write.
	tmp := name + ".part"
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	written, count, err := writeArchive(file, data)
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, name); err != nil {
		return err
	}

	fmt.Printf("%s  %d CVs, %d KB\n", name, count, written/1024)
	if *keep > 0 {
		if err := rotate(name, *keep); err != nil {
			return err
		}
	}
	return nil
}

func writeArchive(w io.Writer, data string) (int64, int, error) {
	gz := gzip.NewWriter(w)
	archive := tar.NewWriter(gz)

	count := 0
	err := filepath.WalkDir(data, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(data, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		// The trash holds what somebody asked to be rid of.
		if strings.HasPrefix(rel, ".trash") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if !strings.HasPrefix(rel, ".") {
				count++
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = rel
		if err := archive.WriteHeader(header); err != nil {
			return err
		}
		source, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(archive, source)
		source.Close()
		return err
	})
	if err != nil {
		return 0, 0, err
	}
	if err := archive.Close(); err != nil {
		return 0, 0, err
	}
	if err := gz.Close(); err != nil {
		return 0, 0, err
	}
	if f, ok := w.(*os.File); ok {
		if info, err := f.Stat(); err == nil {
			return info.Size(), count, nil
		}
	}
	return 0, count, nil
}

// rotate keeps the newest few backups beside this one and removes the rest.
//
// By NAME rather than by modification time: the name carries the date it was
// taken, and a file copied between machines keeps its name while losing its
// timestamp — so sorting by time would delete the wrong ones exactly when the
// backups have been moved somewhere safe.
func rotate(newest string, keep int) error {
	dir := filepath.Dir(newest)
	base := filepath.Base(newest)

	// Everything up to the first "-", which is what a dated name has before its
	// date. A name with no dash at all — `t.tar.gz`, or anything somebody chose
	// — has no prefix to match on, and the whole name is used instead.
	//
	// This was `base[:strings.Index(base+"-", "-")+1]`, which appends a dash so
	// that Index always finds one. It does, at the END, and the slice then runs
	// one past the string: `picvert backup --out t.tar.gz --keep 14` panicked
	// with "slice bounds out of range". Found by running a backup with a name
	// that was not the default one.
	prefix := base
	if at := strings.Index(base, "-"); at >= 0 {
		prefix = base[:at+1]
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var found []string
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && strings.HasPrefix(name, prefix) && strings.HasSuffix(name, ".tar.gz") {
			found = append(found, name)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(found)))
	for _, name := range found[min(keep, len(found)):] {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return err
		}
		fmt.Printf("  removed %s\n", name)
	}
	return nil
}

// restoreCmd puts a backup back.
//
// It REFUSES to write into a data directory that already holds CVs, and that
// refusal is the feature. Restoring is something done in a hurry, on a bad
// day, and merging an old backup into a live service produces a set of CVs
// that existed at no single moment — some current, some from last week, and
// no way to tell which is which afterwards.
func restoreCmd(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	from := fs.String("from", "", "the backup to read")
	dir := fs.String("data", "", "where to put it (default: the configured directory)")
	force := fs.Bool("force", false, "restore even if CVs are already there")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *from == "" {
		return fmt.Errorf("--from is required")
	}

	data := *dir
	if data == "" {
		data = configuredDataDir()
	}

	if !*force {
		if entries, err := os.ReadDir(data); err == nil {
			for _, entry := range entries {
				if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
					return fmt.Errorf(
						"%s already holds CVs — restoring would mix them with the "+
							"backup's.\nMove it aside first, or pass --force if that is "+
							"what you meant", data)
				}
			}
		}
	}

	file, err := os.Open(*from)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("%s is not a piCVert backup: %w", *from, err)
	}
	defer gz.Close()

	if err := os.MkdirAll(data, 0o700); err != nil {
		return err
	}
	archive := tar.NewReader(gz)
	count := 0
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		// A tar can name anything it likes, including "../../etc/passwd". This
		// one was written by us, but a restore reads a file somebody may have
		// been handed — and the cost of checking is one line.
		target := filepath.Join(data, filepath.Clean("/"+header.Name))
		if !strings.HasPrefix(target, filepath.Clean(data)+string(os.PathSeparator)) {
			return fmt.Errorf("the backup names a path outside the data directory: %q",
				header.Name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC,
			os.FileMode(header.Mode)&0o777)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, archive); err != nil {
			out.Close()
			return err
		}
		out.Close()
		count++
	}
	fmt.Printf("restored %d files into %s\n", count, data)
	return nil
}
