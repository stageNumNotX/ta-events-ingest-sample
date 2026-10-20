package traffic

// PreparedRelease — неизменяемый снимок, который request path захватывает
// один раз через current.Load(). Поля не экспортируют изменяемые коллекции.
type PreparedRelease struct {
	Name   string
	Target string
}

func (r *PreparedRelease) valid() bool {
	return r != nil && r.Target != ""
}
