// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"flag"
	"fmt"
	"reflect"
	. "runtime"
	"testing"
	"time"
	"unsafe"
)

func TestMemStats(t *testing.T) {
	// Make sure there's at least one forced GC.
	GC()

	// Test that MemStats has sane values.
	st := new(MemStats)
	ReadMemStats(st)

	nz := func(x interface{}) error {
		if x != reflect.Zero(reflect.TypeOf(x)).Interface() {
			return nil
		}
		return fmt.Errorf("zero value")
	}
	le := func(thresh float64) func(interface{}) error {
		return func(x interface{}) error {
			if reflect.ValueOf(x).Convert(reflect.TypeOf(thresh)).Float() < thresh {
				return nil
			}
			return fmt.Errorf("insanely high value (overflow?); want <= %v", thresh)
		}
	}
	eq := func(x interface{}) func(interface{}) error {
		return func(y interface{}) error {
			if x == y {
				return nil
			}
			return fmt.Errorf("want %v", x)
		}
	}
	// Of the uint fields, HeapReleased, HeapIdle can be 0.
	// PauseTotalNs can be 0 if timer resolution is poor.
	//
	// TODO: Test that GCCPUFraction is <= 0.99. This currently
	// fails on windows/386. (Issue #19319)
	fields := map[string][]func(interface{}) error{
		"Alloc": {nz, le(1e10)}, "TotalAlloc": {nz, le(1e11)}, "Sys": {nz, le(1e10)},
		"Lookups": {nz, le(1e10)}, "Mallocs": {nz, le(1e10)}, "Frees": {nz, le(1e10)},
		"HeapAlloc": {nz, le(1e10)}, "HeapSys": {nz, le(1e10)}, "HeapIdle": {le(1e10)},
		"HeapInuse": {nz, le(1e10)}, "HeapReleased": {le(1e10)}, "HeapObjects": {nz, le(1e10)},
		"StackInuse": {nz, le(1e10)}, "StackSys": {nz, le(1e10)},
		"MSpanInuse": {nz, le(1e10)}, "MSpanSys": {nz, le(1e10)},
		"MCacheInuse": {nz, le(1e10)}, "MCacheSys": {nz, le(1e10)},
		"BuckHashSys": {nz, le(1e10)}, "GCSys": {nz, le(1e10)}, "OtherSys": {nz, le(1e10)},
		"NextGC": {nz, le(1e10)}, "LastGC": {nz},
		"PauseTotalNs": {le(1e11)}, "PauseNs": nil, "PauseEnd": nil,
		"NumGC": {nz, le(1e9)}, "NumForcedGC": {nz, le(1e9)},
		"GCCPUFraction": nil, "EnableGC": {eq(true)}, "DebugGC": {eq(false)},
		"BySize": nil,
	}

	rst := reflect.ValueOf(st).Elem()
	for i := 0; i < rst.Type().NumField(); i++ {
		name, val := rst.Type().Field(i).Name, rst.Field(i).Interface()
		checks, ok := fields[name]
		if !ok {
			t.Errorf("unknown MemStats field %s", name)
			continue
		}
		for _, check := range checks {
			if err := check(val); err != nil {
				t.Errorf("%s = %v: %s", name, val, err)
			}
		}
	}

	if st.Sys != st.HeapSys+st.StackSys+st.MSpanSys+st.MCacheSys+
		st.BuckHashSys+st.GCSys+st.OtherSys {
		t.Fatalf("Bad sys value: %+v", *st)
	}

	if st.HeapIdle+st.HeapInuse != st.HeapSys {
		t.Fatalf("HeapIdle(%d) + HeapInuse(%d) should be equal to HeapSys(%d), but isn't.", st.HeapIdle, st.HeapInuse, st.HeapSys)
	}

	if lpe := st.PauseEnd[int(st.NumGC+255)%len(st.PauseEnd)]; st.LastGC != lpe {
		t.Fatalf("LastGC(%d) != last PauseEnd(%d)", st.LastGC, lpe)
	}

	var pauseTotal uint64
	for _, pause := range st.PauseNs {
		pauseTotal += pause
	}
	if int(st.NumGC) < len(st.PauseNs) {
		// We have all pauses, so this should be exact.
		if st.PauseTotalNs != pauseTotal {
			t.Fatalf("PauseTotalNs(%d) != sum PauseNs(%d)", st.PauseTotalNs, pauseTotal)
		}
		for i := int(st.NumGC); i < len(st.PauseNs); i++ {
			if st.PauseNs[i] != 0 {
				t.Fatalf("Non-zero PauseNs[%d]: %+v", i, st)
			}
			if st.PauseEnd[i] != 0 {
				t.Fatalf("Non-zero PauseEnd[%d]: %+v", i, st)
			}
		}
	} else {
		if st.PauseTotalNs < pauseTotal {
			t.Fatalf("PauseTotalNs(%d) < sum PauseNs(%d)", st.PauseTotalNs, pauseTotal)
		}
	}

	if st.NumForcedGC > st.NumGC {
		t.Fatalf("NumForcedGC(%d) > NumGC(%d)", st.NumForcedGC, st.NumGC)
	}
}

func TestStringConcatenationAllocs(t *testing.T) {
	n := testing.AllocsPerRun(1e3, func() {
		b := make([]byte, 10)
		for i := 0; i < 10; i++ {
			b[i] = byte(i) + '0'
		}
		s := "foo" + string(b)
		if want := "foo0123456789"; s != want {
			t.Fatalf("want %v, got %v", want, s)
		}
	})
	// Only string concatenation allocates.
	if n != 1 {
		t.Fatalf("want 1 allocation, got %v", n)
	}
}

func TestTinyAlloc(t *testing.T) {
	const N = 16
	var v [N]unsafe.Pointer
	for i := range v {
		v[i] = unsafe.Pointer(new(byte))
	}

	chunks := make(map[uintptr]bool, N)
	for _, p := range v {
		chunks[uintptr(p)&^7] = true
	}

	if len(chunks) == N {
		t.Fatal("no bytes allocated within the same 8-byte chunk")
	}
}

var mallocSink uintptr

func BenchmarkMalloc8(b *testing.B) {
	var x uintptr
	for i := 0; i < b.N; i++ {
		p := new(int64)
		x ^= uintptr(unsafe.Pointer(p))
	}
	mallocSink = x
}

func BenchmarkMalloc16(b *testing.B) {
	var x uintptr
	for i := 0; i < b.N; i++ {
		p := new([2]int64)
		x ^= uintptr(unsafe.Pointer(p))
	}
	mallocSink = x
}

func BenchmarkMallocTypeInfo8(b *testing.B) {
	var x uintptr
	for i := 0; i < b.N; i++ {
		p := new(struct {
			p [8 / unsafe.Sizeof(uintptr(0))]*int
		})
		x ^= uintptr(unsafe.Pointer(p))
	}
	mallocSink = x
}

func BenchmarkMallocTypeInfo16(b *testing.B) {
	var x uintptr
	for i := 0; i < b.N; i++ {
		p := new(struct {
			p [16 / unsafe.Sizeof(uintptr(0))]*int
		})
		x ^= uintptr(unsafe.Pointer(p))
	}
	mallocSink = x
}

type LargeStruct struct {
	x [16][]byte
}

func BenchmarkMallocLargeStruct(b *testing.B) {
	var x uintptr
	for i := 0; i < b.N; i++ {
		p := make([]LargeStruct, 2)
		x ^= uintptr(unsafe.Pointer(&p[0]))
	}
	mallocSink = x
}

var n = flag.Int("n", 1000, "number of goroutines")

func BenchmarkGoroutineSelect(b *testing.B) {
	quit := make(chan struct{})
	read := func(ch chan struct{}) {
		for {
			select {
			case _, ok := <-ch:
				if !ok {
					return
				}
			case <-quit:
				return
			}
		}
	}
	benchHelper(b, *n, read)
}

func BenchmarkGoroutineBlocking(b *testing.B) {
	read := func(ch chan struct{}) {
		for {
			if _, ok := <-ch; !ok {
				return
			}
		}
	}
	benchHelper(b, *n, read)
}

func BenchmarkGoroutineForRange(b *testing.B) {
	read := func(ch chan struct{}) {
		for range ch {
		}
	}
	benchHelper(b, *n, read)
}

func benchHelper(b *testing.B, n int, read func(chan struct{})) {
	m := make([]chan struct{}, n)
	for i := range m {
		m[i] = make(chan struct{}, 1)
		go read(m[i])
	}
	b.StopTimer()
	b.ResetTimer()
	GC()

	for i := 0; i < b.N; i++ {
		for _, ch := range m {
			if ch != nil {
				ch <- struct{}{}
			}
		}
		time.Sleep(10 * time.Millisecond)
		b.StartTimer()
		GC()
		b.StopTimer()
	}

	for _, ch := range m {
		close(ch)
	}
	time.Sleep(10 * time.Millisecond)
}

func BenchmarkGoroutineIdle(b *testing.B) {
	quit := make(chan struct{})
	fn := func() {
		<-quit
	}
	for i := 0; i < *n; i++ {
		go fn()
	}

	GC()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		GC()
	}

	b.StopTimer()
	close(quit)
	time.Sleep(10 * time.Millisecond)
}
