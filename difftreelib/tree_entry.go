package difftreelib

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"

	"github.com/deckarep/golang-set" // mapset
)

type resultType int

const (
	kNil resultType = iota
	kPerfectMatch
	kMissing // path is missing in tree2
	kGoodEnough
	kMismatch
	kDifferentTypes
	kDifferentPermissions
	kDirSameEntries
	kDirDifferentEntries
	kError
	kIgnored
)

type treeEntry struct {
	order       int
	path1       string
	path2       string
	info1       os.FileInfo
	info2       os.FileInfo
	hasInfo2    bool
	err         error
	result      resultType
	description string
}

func (self *treeEntry) reset() {
	self.order = 0
	self.path1 = ""
	self.path2 = ""
	self.info1 = nil
	self.info2 = nil
	self.hasInfo2 = false
	self.err = nil
	self.result = kNil
	self.description = ""
}

func (self *treeEntry) computePath2(path1RootLen int, path2Root string) {
	if len(self.path1) > path1RootLen {
		self.path2 = filepath.Join(path2Root, self.path1[path1RootLen:])
	} else {
		self.path2 = path2Root
	}
}

func translateModeType(fileType os.FileMode) string {
	switch fileType {
	case os.ModeDir:
		return "directory"
	case os.ModeSymlink:
		return "symlink"
	case os.ModeNamedPipe:
		return "named pipe"
	case os.ModeSocket:
		return "socket"
	case os.ModeDevice:
		return "device"
	default:
		return "regular file"
	}
}

func (self *treeEntry) comparePaths(options *DifftreeOptions) {
	var statErr error
	if self.result == kIgnored {
		panic(fmt.Sprintf("%s is ignored but compared", self.path1))
	}
	if !self.hasInfo2 {
		self.info2, statErr = os.Lstat(self.path2)
		if statErr != nil {
			// Is path2 missing?
			if os.IsNotExist(statErr) {
				log.Printf("Missing %s", self.path2)
				self.result = kMissing
				return
			}
			// path2 had some other error
			self.err = statErr
			self.result = kError
			return
		}
		// This is not really needed, but it's accurate
		self.hasInfo2 = true
	}

	// Same inode types?
	type1 := self.info1.Mode() & os.ModeType
	type2 := self.info2.Mode() & os.ModeType
	if type1 != type2 {
		self.result = kDifferentTypes
		self.description = fmt.Sprintf("file1 is a %s, but file2 is a %s",
			translateModeType(type1), translateModeType(type2))
		return
	}

	// Same permissions?
	if self.info1.Mode().Perm() != self.info2.Mode().Perm() {
		self.result = kDifferentPermissions
		self.description = fmt.Sprintf("file1 has perms %s, but file2 has %s",
			self.info1.Mode().String(), self.info2.Mode().String())
		return
	}

	// Are these directories?
	if self.info1.IsDir() {
		self.compareDirectories(options)
		return
	}

	// TODO - compare symlinks

	self.compareRegularFiles(options)
}

func readDirectoryIntoSet(directory string, options *DifftreeOptions) (mapset.Set, error) {
	// We don't need locking as we're the only goroutine
	// that will access this set
	set := mapset.NewThreadUnsafeSet()

	dirEntries, err := ioutil.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("ReadDir(%s)", directory)
	}

	for _, dirEntry := range dirEntries {
		if _, has := options.IgnoreFiles[dirEntry.Name()]; has {
			continue
		}
		set.Add(dirEntry.Name())
	}
	return set, nil
}

func createEnumeratedList(set mapset.Set) string {
	var text string

	for i, item := range set.ToSlice() {
		text += fmt.Sprintf("    %4d. %s\n", i+1, item)
	}
	return text
}

func (self *treeEntry) compareDirectories(options *DifftreeOptions) {

	dir1Set, err := readDirectoryIntoSet(self.path1, options)
	if err != nil {
		self.result = kError
		self.err = err
		return
	}

	dir2Set, err := readDirectoryIntoSet(self.path2, options)
	if err != nil {
		self.result = kError
		self.err = err
		return
	}

	if dir1Set.Equal(dir2Set) {
		self.result = kDirSameEntries
		return
	}

	self.result = kDirDifferentEntries
	dir1extra := dir1Set.Difference(dir2Set)
	dir2extra := dir2Set.Difference(dir1Set)

	self.description = ""
	if dir1extra.Cardinality() > 0 {
		self.description += "dir1 has these extra entries that are missing from dir2:\n"
		self.description += createEnumeratedList(dir1extra) + "\n"
	}

	if dir2extra.Cardinality() > 0 {
		self.description += "dir2 has these extra entries that are missing from dir1:\n"
		self.description += createEnumeratedList(dir2extra) + "\n"
	}
}

// While sha1 is cryptographically insecure, we don't care,
// as we're only checking between two trees that we own.
// Plus, it's faster than sha256
func getFileHash(filename string) ([]byte, error) {
	hasher := sha1.New()
	f, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("Opening %e for hashing: %q",
			filename, err)
	}
	defer f.Close()

	_, err = io.Copy(hasher, f)
	if err != nil {
		return nil, fmt.Errorf("Reading %e for hashing: %q",
			filename, err)
	}
	return hasher.Sum(nil), nil
}

func cmpByteSlices(s1 []byte, s2 []byte) bool {
	if len(s1) != len(s2) {
		return false
	}
	for i := 0; i < len(s1); i++ {
		if s1[i] != s2[i] {
			return false
		}
	}
	return true
}

func (self *treeEntry) compareRegularFiles(options *DifftreeOptions) {
	// Does the size match? If not, it's immediately a mismatch,
	// although in the future we could have smart plugins that
	// examine only pertitenent parts of a file (like, ignoring
	// .debug sections of ELF files)
	if self.info1.Size() != self.info2.Size() {
		self.description = fmt.Sprintf(
			"file1 is size %d, file2 is size %d",
			self.info1.Size(), self.info2.Size())
		self.result = kMismatch
		return
	}

	// Same size.... but same contents?
	if options.CheckHashes {
		hash1, err := getFileHash(self.path1)
		if err != nil {
			self.result = kError
			self.err = err
		}
		hash2, err := getFileHash(self.path2)
		if err != nil {
			self.result = kError
			self.err = err
		}

		if cmpByteSlices(hash1, hash2) {
			self.result = kPerfectMatch
		} else {
			self.result = kMismatch
			self.description = fmt.Sprintf(
				"file1 hash SHA1 %s, file2 has SHA1 %s",
				hex.EncodeToString(hash1),
				hex.EncodeToString(hash2))
		}
	} else {
		self.result = kPerfectMatch
	}
}
