package sql

import (
	"strings"
)

type sqlite struct{}

func (*sqlite) Concat(s ...string) string {
	return strings.Join(s, " || ")
}

func (*sqlite) Hash(s ...string) string {
	return ""
}

func (*sqlite) HashParent(name string, s ...string) string {
	return ""
}
