package somepkg

// Thing is an interface
type Thing interface {

	// Do does a thing
	Do(s Stuff) Stuff
}

// Stuff is a struct
type Stuff struct{}

// ThingImplemented matches Thing interface
type ThingImplemented struct{}

// Do does stuff
func (ti *ThingImplemented) Do(s Stuff) Stuff {
	return s
}
