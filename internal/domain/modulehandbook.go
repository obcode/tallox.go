package domain

import (
	"strconv"
	"strings"
)

// ModuleHandbook is where the examination office publishes its module descriptions: the public
// catalogue next to the REST interface the import reads, one page with a fragment per module.
//
// Derived from the configured interface address rather than configured on its own. The two are
// one installation, and a second key would be a second place to forget when it moves — and the
// address stays out of this repository, which is public, for the reason the interface's does.
// The zero value publishes nothing, which is every DevContainer and every CI run.
type ModuleHandbook struct {
	base string
}

// NewModuleHandbook points at the catalogue of the examination office whose interface is at
// zpaBaseURL — the root, without /rest. Empty means there is no handbook to link to.
func NewModuleHandbook(zpaBaseURL string) ModuleHandbook {
	base := strings.TrimRight(strings.TrimSpace(zpaBaseURL), "/")
	if base == "" {
		return ModuleHandbook{}
	}
	return ModuleHandbook{base: base + "/pub/"}
}

// URL is the module's page in the handbook, and whether it has one.
//
// Only an imported module that the examination office still offers. The public catalogue lists
// the active modules and nothing else — about four in five — and a link to one it has dropped
// opens an empty panel titled "Modul", which reads as a broken link rather than as an answer. A
// local module has no page anywhere: the faculty entered it, the examination office does not
// know it.
func (h ModuleHandbook) URL(m Module) (string, bool) {
	if h.base == "" || m.Source != ModuleSourceZPA || m.ZpaID == nil || !m.Active || m.RetiredAt != nil {
		return "", false
	}
	return h.base + "#m=" + strconv.FormatInt(*m.ZpaID, 10), true
}
