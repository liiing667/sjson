package sjson

// This file pins down edge-case behavior that the config hot-reload path
// relies on. Each test documents *current* behavior of the library; where
// the behavior is surprising or hazardous, the expectation is recorded in
// comments instead of changing the implementation.

import (
	"bytes"
	"sync"
	"testing"

	"github.com/tidwall/gjson"
)

// 顶层数组下标越界时的替换语义：
// 越界下标不报错，而是用 null 把数组补齐到目标下标后再写入。
func TestEdgeTopLevelArrayOutOfRange(t *testing.T) {
	// 越界写入：中间空洞用 null 填充。
	got, err := SetBytes([]byte(`[1,2,3]`), "5", 9)
	if err != nil {
		t.Fatalf("out-of-range set returned error: %v", err)
	}
	if string(got) != `[1,2,3,null,null,9]` {
		t.Fatalf("expected null-filled array, got %s", got)
	}

	// 范围内下标是就地替换，不改变长度。
	got, err = SetBytes([]byte(`[1,2,3]`), "1", 9)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `[1,9,3]` {
		t.Fatalf("expected in-place element replace, got %s", got)
	}

	// "-1" 是追加语义，不是越界。
	got, err = SetBytes([]byte(`[1,2,3]`), "-1", 9)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `[1,2,3,9]` {
		t.Fatalf("expected append, got %s", got)
	}

	// 删除越界下标：原样返回，不报错。
	got, err = DeleteBytes([]byte(`[1,2,3]`), "9")
	if err != nil {
		t.Fatalf("out-of-range delete returned error: %v", err)
	}
	if string(got) != `[1,2,3]` {
		t.Fatalf("expected unchanged input, got %s", got)
	}

	// 删除范围内下标：元素被移除，数组收缩。
	got, err = DeleteBytes([]byte(`[1,2,3]`), "1")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `[1,3]` {
		t.Fatalf("expected element removal, got %s", got)
	}
}

// SetBytes 与 Set 在数字、布尔、null 字面量上的行为必须一致；
// 真正的坑在于 Go 类型到 JSON 字面量的映射：
// nil 写成 null，字符串 "1"/"true"/"null" 写成带引号的 JSON 字符串。
func TestEdgeSetVsSetBytesLiterals(t *testing.T) {
	cases := []struct {
		value  interface{}
		expect string
	}{
		{nil, `{"k":null}`},      // nil -> JSON null
		{1, `{"k":1}`},           // 数字不带引号
		{-3, `{"k":-3}`},         //
		{1.5, `{"k":1.5}`},       //
		{true, `{"k":true}`},     // 布尔不带引号
		{false, `{"k":false}`},   //
		{"1", `{"k":"1"}`},       // 字符串形式的字面量会被引号包起来
		{"true", `{"k":"true"}`}, //
		{"null", `{"k":"null"}`}, //
	}
	for _, c := range cases {
		s, err := Set(`{}`, "k", c.value)
		if err != nil {
			t.Fatalf("Set(%#v): %v", c.value, err)
		}
		if s != c.expect {
			t.Fatalf("Set(%#v): expected %s, got %s", c.value, c.expect, s)
		}
		b, err := SetBytes([]byte(`{}`), "k", c.value)
		if err != nil {
			t.Fatalf("SetBytes(%#v): %v", c.value, err)
		}
		if string(b) != c.expect {
			t.Fatalf("SetBytes(%#v): expected %s, got %s", c.value, c.expect, b)
		}
		// 两个入口对同一字面量必须产出完全相同的字节。
		if s != string(b) {
			t.Fatalf("Set/SetBytes mismatch for %#v: %s vs %s", c.value, s, b)
		}
	}

	// SetBytes 传 []byte 时按字符串处理（带引号），不是按 raw JSON 嵌入。
	b, err := SetBytes([]byte(`{}`), "k", []byte("1"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"k":"1"}` {
		t.Fatalf("expected []byte to be stringified, got %s", b)
	}
}

// 替换语义：本版本没有 sjson.Replace 这个 API（热更新代码里的
// sjson.Replace 是封装或笔误），与之最接近的是 Set 的两种路径形态：
//   - 精确路径（a.0.b）只替换第一个/唯一匹配；
//   - 通配路径（a.#.b）替换全部匹配。
//
// 这里把两种语义都钉死；如果以后真的引入 Replace，需要单独决定它对齐哪一种。
func TestEdgeReplaceFirstVsAll(t *testing.T) {
	const in = `{"a":[{"b":1},{"b":2}]}`

	// 精确下标：只动第一个元素。
	got, err := Set(in, "a.0.b", 9)
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"a":[{"b":9},{"b":2}]}` {
		t.Fatalf("expected only first match replaced, got %s", got)
	}

	// '#' 通配：所有匹配都被替换。
	got, err = Set(in, "a.#.b", 9)
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"a":[{"b":9},{"b":9}]}` {
		t.Fatalf("expected all matches replaced, got %s", got)
	}
}

// 带转义字符的 key（含 '.' 和 ':'）与 ReplaceInPlace 的交互。
func TestEdgeEscapedKeysAndReplaceInPlace(t *testing.T) {
	opts := &Options{Optimistic: true, ReplaceInPlace: true}

	// key 本身含 '.'：路径里用 '\.' 转义；等长替换时原地写回，
	// 输入缓冲区被直接修改，返回值与输入共享底层数组。
	in := []byte(`{"a.b":{"c":"old"}}`)
	out, err := SetBytesOptions(in, `a\.b.c`, "new", opts)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"a.b":{"c":"new"}}` {
		t.Fatalf("unexpected result %s", out)
	}
	if string(in) != `{"a.b":{"c":"new"}}` {
		t.Fatalf("ReplaceInPlace should mutate input, in=%s", in)
	}
	if &out[0] != &in[0] {
		t.Fatal("ReplaceInPlace should reuse the input backing array")
	}

	// 不加 ReplaceInPlace：结果相同，但输入保持不变。
	in2 := []byte(`{"a.b":{"c":"old"}}`)
	out2, err := SetBytesOptions(in2, `a\.b.c`, "new", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(out2) != `{"a.b":{"c":"new"}}` {
		t.Fatalf("unexpected result %s", out2)
	}
	if string(in2) != `{"a.b":{"c":"old"}}` {
		t.Fatalf("input should be untouched without ReplaceInPlace, in=%s", in2)
	}

	// 替换值更长：底层数组放不下时会重新分配，输入不被修改。
	in3 := []byte(`{"k":"old"}`)
	out3, err := SetBytesOptions(in3, "k", "muchlongervalue", opts)
	if err != nil {
		t.Fatal(err)
	}
	if string(out3) != `{"k":"muchlongervalue"}` {
		t.Fatalf("unexpected result %s", out3)
	}
	if string(in3) != `{"k":"old"}` {
		t.Fatalf("realloc path should leave input intact, in=%s", in3)
	}

	// 替换值更短：返回值有效，但输入缓冲区尾部会残留被截断的
	// 旧字节——这是 Options 文档里 "the input json will not longer be
	// valid" 的具体含义。热更新侧不能再把 in4 当 JSON 用。
	in4 := []byte(`{"k":"longvalue"}`)
	out4, err := SetBytesOptions(in4, "k", "s", opts)
	if err != nil {
		t.Fatal(err)
	}
	if string(out4) != `{"k":"s"}` {
		t.Fatalf("unexpected result %s", out4)
	}
	if gjson.ValidBytes(in4) {
		t.Fatalf("input should be corrupted after shorter in-place replace, in=%s", in4)
	}

	// key 含 ':'：':' 在路径里不是特殊字符，不需要转义；
	// 写成 '\:' 也能工作（反斜杠被原样剥离）。
	in5 := []byte(`{"a:b":1}`)
	out5, err := SetBytesOptions(in5, `a:b`, 2, opts)
	if err != nil {
		t.Fatal(err)
	}
	if string(out5) != `{"a:b":2}` {
		t.Fatalf("unexpected result %s", out5)
	}
	out5, err = SetBytes([]byte(`{"a:b":1}`), `a\:b`, 2)
	if err != nil {
		t.Fatal(err)
	}
	if string(out5) != `{"a:b":2}` {
		t.Fatalf("unexpected result for escaped colon %s", out5)
	}

	// ':' 前缀强制把数字段当作字符串 key：在空对象上 ':0' 与 '0'
	// 都建出对象 key "0"（因为现有值是对象）；但在已存在的数组上，
	// ':0' 仍然命中下标 0——force 只影响“新建路径”的形态，
	// 不影响对已有数组的查找。这点容易踩，钉死。
	got, err := Set(`{}`, ":0", "y")
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"0":"y"}` {
		t.Fatalf("expected object key, got %s", got)
	}
	got, err = Set(`["a","b"]`, ":0", "y")
	if err != nil {
		t.Fatal(err)
	}
	if got != `["y","b"]` {
		t.Fatalf("force prefix should not change array lookup, got %s", got)
	}
}

// 删除不存在的路径：返回的字节切片与输入共享同一个底层数组，
// 内容逐字节相等，error 为 nil——调用方可以安全地继续共享/复用输入。
func TestEdgeDeleteMissingPathSharesInput(t *testing.T) {
	in := []byte(`{"a":1}`)

	out, err := DeleteBytes(in, "b")
	if err != nil {
		t.Fatalf("delete of missing path should not error, got %v", err)
	}
	if !bytes.Equal(out, in) {
		t.Fatalf("expected unchanged bytes, got %s", out)
	}
	if &out[0] != &in[0] {
		t.Fatal("expected output to alias the input backing array")
	}

	// 不存在的嵌套路径同样原样返回。
	out, err = DeleteBytes(in, "a.b.c")
	if err != nil {
		t.Fatal(err)
	}
	if &out[0] != &in[0] || !bytes.Equal(out, in) {
		t.Fatalf("expected aliased unchanged input, got %s", out)
	}

	// 字符串入口行为一致：内容不变、不报错。
	s, err := Delete(`{"a":1}`, "b")
	if err != nil {
		t.Fatal(err)
	}
	if s != `{"a":1}` {
		t.Fatalf("expected unchanged json, got %s", s)
	}
}

// 并发读安全性：只要写侧不使用 ReplaceInPlace，SetBytes 不会修改
// 输入缓冲区，任意数量的 goroutine 可以同时用 gjson.Get 读同一份
// []byte。用 -race 跑本测试来回归这条保证。
func TestEdgeConcurrentGetWhileSet(t *testing.T) {
	shared := []byte(`{"a":{"b":1}}`)

	const readers = 4
	const iterations = 2000

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				res := gjson.GetBytes(shared, "a.b")
				// 写侧若原地修改 shared，这里会读到撕裂或变化的值。
				if !res.Exists() || res.Int() != 1 {
					t.Errorf("reader observed corrupted shared buffer: %v", res)
					return
				}
			}
		}()
	}

	// 写侧：不带 ReplaceInPlace 的 SetBytes 必须分配新切片，
	// 不允许碰 shared。
	cur := shared
	for i := 0; i < 500; i++ {
		next, err := SetBytes(cur, "a.b", i)
		if err != nil {
			t.Fatal(err)
		}
		cur = next
	}
	close(stop)
	wg.Wait()

	// 写完后原始输入必须一字未动。
	if string(shared) != `{"a":{"b":1}}` {
		t.Fatalf("shared buffer was mutated by SetBytes: %s", shared)
	}
}

// 对照组（确定性，不依赖调度）：ReplaceInPlace 会直接改写共享缓冲区，
// 所以它与“并发读同一份 []byte”不兼容。这里不构造数据竞争，
// 只钉死“输入会被写穿”这一事实；热更新路径如果在用 ReplaceInPlace，
// 读侧必须先拷贝。
func TestEdgeReplaceInPlaceMutatesSharedBuffer(t *testing.T) {
	shared := []byte(`{"a":{"b":111}}`)
	_, err := SetBytesOptions(shared, "a.b", 222,
		&Options{Optimistic: true, ReplaceInPlace: true})
	if err != nil {
		t.Fatal(err)
	}
	if string(shared) != `{"a":{"b":222}}` {
		t.Fatalf("expected shared buffer to be written through, got %s", shared)
	}
}
