package difftreelib

import (
	"fmt"
)

type ComparisonEngine struct {
	countPerfectMatch   int
	countError          int
	countDifferentTypes int
	countDifferentPerms int
	countMismatch       int
	countMissing        int
	countDirSame        int
	countDirDifferent   int
	countIgnoredByUser  int

	path1RootLen int
}

func (s *ComparisonEngine) Summarize() {

	fmt.Printf(`SUMMARY
========================================
# Perfect Matches:              %8d
# Mismatches:                   %8d DTMismatch
# Missing:                      %8d DTMissing
# Different Types:              %8d DTDiffTypes
# Different Perms:              %8d DTDiffPerms
# Ignored (by user):            %8d DTIgnored
# Errors while reading:         %8d DTError

# Dirs with same entries:       %8d
# Dirs with different entries:  %8d DTDiffEntries
`,
		s.countPerfectMatch,
		s.countMismatch,
		s.countMissing,
		s.countDifferentTypes,
		s.countDifferentPerms,
		s.countIgnoredByUser,
		s.countError,
		s.countDirSame,
		s.countDirDifferent)
}
