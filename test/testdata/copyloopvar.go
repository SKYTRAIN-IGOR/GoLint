//golangcitest:args -Ecopyloopvar
package testdata

func copyloopvarCase1() {
	slice := []int{1, 2, 3}
	fns := make([]func(), 0, len(slice)*2)
	for i, v := range slice {
		i := i // want `It's unnecessary to copy the loop variable "i"`
		fns = append(fns, func() {
			_ = i
		})
		_v := v // want `It's unnecessary to copy the loop variable "v"`
		fns = append(fns, func() {
			_ = _v
		})
	}
	for _, fn := range fns {
		fn()
	}
}

func copyloopvarCase2() {
	loopCount := 3
	fns := make([]func(), 0, loopCount)
	for i := 1; i <= loopCount; i++ {
		i := i // want `It's unnecessary to copy the loop variable "i"`
		fns = append(fns, func() {
			_ = i
		})
	}
	for _, fn := range fns {
		fn()
	}
}
