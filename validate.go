package zbft

import (
	"errors"

	"github.com/hexablock/blockchain/bcpb"
)

// defaultBlockValidator validates the N, S, Q values as well as the ProposerIndex
func defaultBlockValidator(header *bcpb.BlockHeader) error {
	if int32(len(header.Signers)) != header.N {
		return errors.New("not enough signers")
	}

	if header.ProposerIndex >= int32(len(header.Signers)) {
		return errors.New("invalid proposer index")
	}

	q := (header.N / 2) + 1
	if header.S < q {
		return errors.New("invalid number of required signatures")
	}
	if header.Q < q {
		return errors.New("invalid number of required commits")
	}

	return nil
}
