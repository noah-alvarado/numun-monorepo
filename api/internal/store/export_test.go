package store

// SetListAllPageSize shrinks ListAllDelegationsInConference's page size so a
// test can cross page boundaries with a few rows. Returns a restore func.
func SetListAllPageSize(n int32) func() {
	prev := listAllPageSize
	listAllPageSize = n
	return func() { listAllPageSize = prev }
}
