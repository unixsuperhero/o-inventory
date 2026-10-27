// Package rt stands in for ochazuke: just the shapes the scan reads.
package rt

type Ctx struct{ Name string }

func (Ctx) Status(string) {}

type Result struct{ Err error }

func Failed(err error) Result { return Result{Err: err} }

type Runtime struct{}

type NounBuilder struct{}

func (*Runtime) Define(string) *NounBuilder                     { return &NounBuilder{} }
func (b *NounBuilder) Verb(string, any, ...string) *NounBuilder { return b }
func (b *NounBuilder) Quiet() *NounBuilder                      { return b }

type Operation struct {
	Noun, Verb string
	Run        func() (any, error)
}

func (*Runtime) Register(Operation) {}
