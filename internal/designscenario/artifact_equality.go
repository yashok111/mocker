package designscenario

// SameContractDocument uses the same lossless JSON equality policy as linked
// contract authoring. Formatting and object key order do not matter; numeric
// spellings and distinct integers remain distinct.
func SameContractDocument(left, right []byte) (bool, error) {
	return equalJSON(left, right)
}
