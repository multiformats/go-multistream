package multistream

import (
	"crypto/sha256"
)

type nodeProtocol[T StringLike] struct {
	protocolID   T
	tombstoneBit bool
}

type abbrevTree[T StringLike] struct {
	root *abbrevNode[T]
}

type abbrevNode[T StringLike] struct {
	p        *nodeProtocol[T]
	children [256]*abbrevNode[T]
}

func (at *abbrevTree[T]) Abbreviate(pid T) []byte {
	var result []byte
	hash := sha256.Sum256([]byte(pid))

	if at.root == nil {
		return nil
	}

	current := at.root
	// go furthest in the tree
	for _, b := range hash {
		if current.children[b] != nil {
			result = append(result, b)
			current = current.children[b]
		} else {
			break
		}
	}

	if current.p != nil && current.p.protocolID == pid && !current.p.tombstoneBit {
		return result
	}
	return nil
}

func (at *abbrevTree[T]) GetProtocolID(prefix []byte) (T, error) {
	if at.root == nil {
		return "", ErrUnknownPrefix
	}
	current := at.root
	for _, b := range prefix {
		if current.children[b] == nil {
			return "", ErrUnknownPrefix
		}
		current = current.children[b]
	}
	if current.p == nil {
		return "", ErrUnknownPrefix
	}
	return current.p.protocolID, nil
}

func (at *abbrevTree[T]) AddProtocol(pid T) {
	hash := sha256.Sum256([]byte(pid))

	if at.root == nil {
		at.root = &abbrevNode[T]{}
	}

	current := at.root
	for idx, b := range hash {
		if current.children[b] == nil {
			current.children[b] = &abbrevNode[T]{
				p: &nodeProtocol[T]{
					protocolID:   pid,
					tombstoneBit: false,
				},
			}
			return
		}
		current = current.children[b]

		if current.p != nil {
			if current.p.protocolID == pid {
				// Resurrect the protocol ID.
				current.p.tombstoneBit = false
			} else if !current.p.tombstoneBit {
				// There is another protocol in this node, so we need to duplicate it down.
				h := sha256.Sum256([]byte(current.p.protocolID))

				if current.children[h[idx+1]] == nil {
					// It should be fine to reference the same nodeProtocol instance.
					current.children[h[idx+1]] = &abbrevNode[T]{p: current.p}
				}
			}
		}
	}
}

func (at *abbrevTree[T]) RemoveProtocol(pid T) {
	hash := sha256.Sum256([]byte(pid))

	if at.root == nil {
		return
	}
	current := at.root
	for _, b := range hash {
		if current.children[b] == nil {
			break
		}
		current = current.children[b]

		if current.p.protocolID == pid {
			current.p.tombstoneBit = true
		}
	}
}
