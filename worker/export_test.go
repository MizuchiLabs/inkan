package worker

// The ledger imports this package, so the test that compares the two lives
// outside of it and borrows these.
var (
	FakeStripe = fakeStripe
	TestConfig = testConfig
	Post       = post
)
