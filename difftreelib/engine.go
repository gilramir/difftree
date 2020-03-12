package difftreelib

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sync"
)

/*
                           +-----------------------------+
                           |                             |
                +----------v----------+                  |
                |  Read tree entries  |                  |
                +---------------------+                  |
                           |                             |
        +------------------+------------------+          |
        |                  |                  |          |
  +-----v-----+      +-----v-----+      +-----v-----+    |
  |  Compare  |      |  Compare  |      |  Compare  |    |
  +-----------+      +-----------+      +-----------+    |
        |                  |                  |          |
        +------------------+------------------+          |
                           |                             |
                  +--------v--------+                    |
                  |  Merge Channels |                    |
                  +-----------------+                    |
                           |                             |
                     +-----v----+                        |
                     |  Report  +------------------------+
                     +----------+
*/

type DifftreeOptions struct {
	CheckHashes bool
	IgnoreFiles map[string]bool
}

func (s *ComparisonEngine) Compare(path1 string, path2 string, options *DifftreeOptions) error {

	// No trailing slashes, etc.
	path1 = path.Clean(path1)
	path2 = path.Clean(path2)

	numWorkers := runtime.NumCPU()

	// numWorkers + 1 for ReadTreeEntries + 1 for Report
	numTreeEntries := numWorkers + 2
	blankEntryChan := make(chan *treeEntry, numTreeEntries)
	filledEntryChan := make(chan *treeEntry, numWorkers)

	// The length of path1, plus 1 for an additional path separator
	s.path1RootLen = len(path1) + 1
	// Account for any path separators at the end
	for i := len(path1) - 1; i >= 0; i-- {
		if path1[1] == filepath.Separator {
			s.path1RootLen--
		} else {
			break
		}
	}

	// Create the comparison workers
	responseChans := make([]chan *treeEntry, numWorkers)
	for i := 0; i < numWorkers; i++ {
		responseChan := make(chan *treeEntry)
		responseChans[i] = responseChan
		go s.compareEntries(path2, filledEntryChan, responseChan, options)
	}

	// Create the go routine that merges the responsee
	singleResponseChan := s.mergeResponseChans(responseChans)

	// Create the go routine that reads the tree entries
	go s.readTreeEntries(path1, path2, blankEntryChan, filledEntryChan, options)

	// Queue the blank tree entries
	// There is a fixed number of treeEntries
	// that can be in flight at a time. We
	// allocate all of them here, to avoid
	// garbage collection
	treeEntries := make([]treeEntry, numTreeEntries)
	for i := 0; i < numTreeEntries; i++ {
		blankEntryChan <- &treeEntries[i]
	}

	// Run the report function
	return s.reportResults(singleResponseChan, blankEntryChan)
}

func (s *ComparisonEngine) readTreeEntries(path1 string, path2 string,
	blankEntryChan chan *treeEntry, filledEntryChan chan *treeEntry, options *DifftreeOptions) {

	defer close(filledEntryChan)

	var order int

	/* (void) */
	filepath.Walk(path1, func(path string, info os.FileInfo, err error) error {
		// Get a blank treeEntry
		entry, ok := <-blankEntryChan
		defer func() {
			log.Printf("Walked onto %s", entry.path1)
			filledEntryChan <- entry
		}()

		if !ok {
			entry.err = errors.New("Couldn't read blankEntryChan")
			// Stop processing.
			return entry.err
		}

		entry.path1 = path
		entry.order = order
		entry.info1 = info
		order++

		// Was there an error while walking?
		if err != nil {
			entry.result = kError
			entry.description = fmt.Sprintf("While walking onto %s: %v", path, err)
			// Keep going
			return nil
		}

		// Should we skip it?
		basename := filepath.Base(path)
		if _, has := options.IgnoreFiles[basename]; has {
			entry.result = kIgnored
			if info.IsDir() {
				// Don't descend into "path" (a directory)
				return filepath.SkipDir
			} else {
				// Keep going
				return nil
			}
		}

		// If path is a dir, does path2's path exist? If not, skip.
		if info.IsDir() {
			var statErr error
			entry.computePath2(s.path1RootLen, path2)
			entry.info2, statErr = os.Lstat(entry.path2)
			if statErr == nil {
				entry.hasInfo2 = true
				if !entry.info2.IsDir() {
					// Don't descend into "path" (a directory)
					return filepath.SkipDir
				}
			}
		}
		// nil == keep going
		return nil
	})
}

func (s *ComparisonEngine) compareEntries(path2 string, entryChan chan *treeEntry,
	responseChan chan *treeEntry, options *DifftreeOptions) {
	defer close(responseChan)

	for entry := range entryChan {
		if entry.result == kIgnored {
			responseChan <- entry
			continue
		}

		if !entry.hasInfo2 {
			entry.computePath2(s.path1RootLen, path2)
			// entry.comparePaths() will do the os.Lstat()
		}

		entry.comparePaths(options)
		responseChan <- entry
	}
}

func (s *ComparisonEngine) mergeResponseChans(
	incomingResponseChans []chan *treeEntry) chan *treeEntry {

	var wg sync.WaitGroup
	out := make(chan *treeEntry)

	// Start an output goroutine for each input channel.
	// It will copy values until the input channel is closed
	outputFunc := func(c <-chan *treeEntry) {
		for n := range c {
			out <- n
		}
		wg.Done()
	}
	wg.Add(len(incomingResponseChans))
	for _, c := range incomingResponseChans {
		go outputFunc(c)
	}

	// Start a goroutine to close the output channel once all the output
	// goroutines are done.
	go func() {
		wg.Wait()
		close(out)
	}()
	return out
}

func (s *ComparisonEngine) reportResults(responseChan chan *treeEntry,
	blankEntryChan chan *treeEntry) error {
	defer close(blankEntryChan)

	for entry := range responseChan {
		// TODO(gramirez) - if the order isn't the next sequentially,
		// before the entry and wait for the correct entry

		// Nothing to see here
		if entry.result == kPerfectMatch {
			log.Printf("PerfectMatch: %s", entry.path1)
			s.countPerfectMatch++
		} else {
			var relativePath string
			if len(entry.path1) > s.path1RootLen {
				relativePath = entry.path1[s.path1RootLen:]
			} else {
				relativePath = entry.path1
			}
			switch entry.result {
			case kError:
				fmt.Printf("%s: DTError %v\n\n", relativePath, entry.err)
				s.countError++

			case kMissing:
				fmt.Printf("%s: DTMissing; missing from tree2\n\n", relativePath)
				s.countMissing++

			case kDifferentPermissions:
				fmt.Printf("%s: DTDiffPerms %s\n\n", relativePath, entry.description)
				s.countDifferentPerms++

			case kDifferentTypes:
				fmt.Printf("%s: DTDiffTypes %s\n\n", relativePath, entry.description)
				s.countDifferentTypes++

			case kMismatch:
				fmt.Printf("%s: DTMismatch %s\n\n", relativePath, entry.description)
				s.countMismatch++

			case kIgnored:
				fmt.Printf("%s: DTIgnored\n\n", relativePath)
				s.countIgnoredByUser++

			case kDirSameEntries:
				s.countDirSame++

			case kDirDifferentEntries:
				fmt.Printf("%s: DTDiffEntries\n", relativePath)
				fmt.Print(entry.description)
				fmt.Print("\n")
				s.countDirDifferent++

			default:
				panic(fmt.Sprintf("Got result=%d for path %s", entry.result,
					relativePath))
			}
		}

		// Recycle the treeEntry
		entry.reset()
		blankEntryChan <- entry
	}

	return nil
}
