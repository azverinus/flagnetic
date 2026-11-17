package management

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/azverinus/flagnetic/internal/domain/experiments"
)

// UnitIDList is the unit IDs of a uniformity check. A request body is read
// whole before it is decoded, but decoding one string per entry is what costs
// the heap (an empty entry is three bytes of body and sixteen of string
// header), so the list stops one entry past the most a check takes: enough
// for the check to refuse it, too little to exhaust a replica.
type UnitIDList []string

// UnmarshalJSON reads the array entry by entry and stops once it holds one
// more than a check takes; the rest was already read as valid JSON.
func (l *UnitIDList) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if tok == nil {
		*l = nil
		return nil
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		return errors.New("unit_ids is not an array")
	}
	ids := UnitIDList{}
	for dec.More() && len(ids) <= experiments.MaxUniformityUnitIDs {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		id, ok := tok.(string)
		if !ok {
			return fmt.Errorf("unit_ids entry %d is not a string", len(ids))
		}
		ids = append(ids, id)
	}
	*l = ids
	return nil
}
