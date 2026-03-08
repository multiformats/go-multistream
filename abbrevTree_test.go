package multistream

import (
	"bytes"
	"crypto/sha256"
	"testing"
)

func TestAbbrevTreeAddProtocol(t *testing.T) {
	tree := &abbrevTree[string]{}

	proto1 := "protocol1"
	hash1 := sha256.Sum256([]byte(proto1))
	proto2 := "protocol2"
	hash2 := sha256.Sum256([]byte(proto2))
	proto3 := "protocol251" // this one has the same first byte as "protocol1"
	hash3 := sha256.Sum256([]byte(proto3))

	// make sure we don't make mistakes on the hashes
	if hash1[0] == hash2[0] {
		t.Fatal("the first bytes of hash1 and hash2 should be different")
	}
	if hash1[0] != hash3[0] {
		t.Fatal("the first bytes of hash1 and hash3 should be the same")
	}
	if hash1[1] == hash3[1] {
		t.Fatal("the second bytes of hash1 and hash3 should be different")
	}

	// add only proto1
	tree.AddProtocol(proto1)

	if tree.root == nil {
		t.Fatal("root should not be nil after adding protocol")
	}
	if tree.root.children[hash1[0]] == nil || tree.root.children[hash1[0]].p == nil {
		t.Fatal("the protocol was not added")
	}
	if tree.root.children[hash1[0]].p.protocolID != proto1 {
		t.Fatal("the protocol ID was wrong")
	}
	if tree.root.children[hash1[0]].p.tombstoneBit {
		t.Fatal("tombstoneBit of proto1 must not be set")
	}
	if !bytes.Equal(tree.Abbreviate(proto1), []byte{hash1[0]}) {
		t.Fatal("abbreviation of proto1 is incorrect")
	}

	// also add proto2
	tree.AddProtocol(proto2)

	if tree.root.children[hash2[0]] == nil || tree.root.children[hash2[0]].p == nil {
		t.Fatal("the protocol was not added")
	}
	if tree.root.children[hash2[0]].p.protocolID != proto2 {
		t.Fatal("the protocol ID was wrong")
	}
	if tree.root.children[hash2[0]].p.tombstoneBit {
		t.Fatal("tombstoneBit of proto2 must not be set")
	}
	if !bytes.Equal(tree.Abbreviate(proto2), []byte{hash2[0]}) {
		t.Fatal("abbreviation of proto2 is incorrect")
	}

	// add proto3 which has the same first byte of the hash as proto1
	tree.AddProtocol(proto3)

	n1 := tree.root.children[hash1[0]]
	// the node at the first level should still be proto1
	if n1.p.protocolID != proto1 {
		t.Fatal("the node in the first level should not be modified")
	}
	// proto1 should be duplicated down
	if n1.children[hash1[1]] == nil || n1.children[hash1[1]].p == nil {
		t.Fatal("proto1 was not duplicated")
	}
	if n1.children[hash1[1]].p.tombstoneBit {
		t.Fatal("tombstoneBit of proto1 must not be set")
	}
	if !bytes.Equal(tree.Abbreviate(proto1), []byte{hash1[0], hash1[1]}) {
		t.Fatal("abbreviation of proto1 is incorrect")
	}
	// proto3 should be added in the second level
	if n1.children[hash3[1]] == nil || n1.children[hash3[1]].p == nil {
		t.Fatal("proto3 was not added")
	}
	if n1.children[hash3[1]].p.tombstoneBit {
		t.Fatal("tombstoneBit of proto3 must not be set")
	}
	if !bytes.Equal(tree.Abbreviate(proto3), []byte{hash3[0], hash3[1]}) {
		t.Fatal("abbreviation of proto3 is incorrect")
	}
}

func TestAbbrevTreeRemoveProtocol(t *testing.T) {
	tree := &abbrevTree[string]{}

	proto1 := "protocol1"
	hash1 := sha256.Sum256([]byte(proto1))
	proto2 := "protocol2"
	proto3 := "protocol251" // this one has the same first byte as "protocol1"

	tree.AddProtocol(proto1)
	tree.AddProtocol(proto2)
	tree.AddProtocol(proto3)

	// remove only proto1
	tree.RemoveProtocol(proto1)

	n1 := tree.root.children[hash1[0]]
	if !n1.p.tombstoneBit {
		t.Fatal("tombstoneBit of proto1 must be set")
	}
	if !n1.children[hash1[1]].p.tombstoneBit {
		t.Fatal("tombstoneBit of proto1 must be set")
	}
	if tree.Abbreviate(proto1) != nil {
		t.Fatal("abbreviation of proto1 should be nil")
	}
}

func TestAbbrevTreeResurrectProtocol(t *testing.T) {
	tree := &abbrevTree[string]{}

	proto1 := "protocol1"
	hash1 := sha256.Sum256([]byte(proto1))
	proto2 := "protocol2"
	proto3 := "protocol251" // this one has the same first byte as "protocol1"

	tree.AddProtocol(proto1)
	tree.AddProtocol(proto2)
	tree.AddProtocol(proto3)
	tree.RemoveProtocol(proto1)
	tree.AddProtocol(proto1)

	n1 := tree.root.children[hash1[0]]
	if n1.p.tombstoneBit {
		t.Fatal("tombstoneBit of proto1 must not be set")
	}
	if n1.children[hash1[1]].p.tombstoneBit {
		t.Fatal("tombstoneBit of proto1 must not be set")
	}

	// There should be another leaf node added for proto1
	n2 := n1.children[hash1[1]]
	if n2.children[hash1[2]] == nil || n2.children[hash1[2]].p == nil {
		t.Fatal("proto1 was not added")
	}
	if n2.children[hash1[2]].p.tombstoneBit {
		t.Fatal("tombstoneBit of proto1 must not be set")
	}
	if !bytes.Equal(tree.Abbreviate(proto1), []byte{hash1[0], hash1[1], hash1[2]}) {
		t.Fatal("abbreviation of proto1 is incorrect")
	}
}
