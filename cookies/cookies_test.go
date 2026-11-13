package cookies

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/xpzouying/xiaohongshu-mcp/internal/testutil"
)

// TestGetCookiesFilePath 校验 cookies 文件固定在数据根下。
func TestGetCookiesFilePath(t *testing.T) {
	root := testutil.IsolateDataRoot(t)
	t.Setenv("COOKIES_PATH", filepath.Join(t.TempDir(), "ignored.json"))
	path, err := filepath.Abs(GetCookiesFilePath())
	assert.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "cookies.json"), path)
}

// TestLoadSaveDeleteCookies 校验 cookie 文件存取往返与删除的幂等。
func TestLoadSaveDeleteCookies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.json")
	c := NewLoadCookie(path)

	// 未写入时读取应报错
	_, err := c.LoadCookies()
	assert.Error(t, err)

	// 写入后能读回同样的内容（落盘是 v2，排版会变，内容不变）
	want := []byte(`[{"name":"web_session","value":"x"}]`)
	assert.NoError(t, c.SaveCookies(want))
	got, err := c.LoadCookies()
	assert.NoError(t, err)
	assert.Equal(t, decodeJSON(t, want), decodeJSON(t, got))

	// 落盘是 v2 外层对象，不再是裸数组。只看结构，不看排版
	onDisk, err := os.ReadFile(path)
	assert.NoError(t, err)
	var f map[string]any
	assert.NoError(t, json.Unmarshal(onDisk, &f))
	assert.Equal(t, float64(2), f["version"])

	// 删除后文件消失，且再次删除幂等（不报错）
	assert.NoError(t, c.DeleteCookies())
	assert.NoFileExists(t, path)
	assert.NoError(t, c.DeleteCookies())
}

// TestSeed 校验 seed 的持久化与 v1/v2 两种格式的读取。
func TestSeed(t *testing.T) {
	t.Run("v1裸数组读不到seed且cookies原样", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "cookies.json")
		raw := []byte(`[{"name":"web_session","value":"x"}]`)
		assert.NoError(t, os.WriteFile(path, raw, 0644))

		c := NewLoadCookie(path)

		got, err := c.LoadCookies()
		assert.NoError(t, err)
		assert.Equal(t, decodeJSON(t, raw), decodeJSON(t, got))
		assert.Equal(t, 0, c.LoadSeed())
	})

	t.Run("存seed后读得回来且cookies内容不走样", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "cookies.json")
		c := NewLoadCookie(path)

		// 字段顺序刻意打乱、数值用科学计数法，用来暴露"反序列化再序列化"造成的走样
		raw := []byte(`[{"value":"x","name":"web_session","expires":1.75e9}]`)
		assert.NoError(t, c.SaveCookies(raw))
		assert.NoError(t, c.SaveSeed(23088))

		assert.Equal(t, 23088, c.LoadSeed())

		got, err := c.LoadCookies()
		assert.NoError(t, err)
		// 比语义不比字节：落盘会重新缩进（只动无意义空白），字段和取值一个都不能变
		assert.Equal(t, decodeJSON(t, raw), decodeJSON(t, got))
	})
}

// TestSeedRobustness 校验 seed 在异常与并发写入下的表现。
func TestSeedRobustness(t *testing.T) {
	t.Run("存cookies不冲掉已有的seed", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "cookies.json")
		c := NewLoadCookie(path)

		assert.NoError(t, c.SaveSeed(23088))
		// 模拟重新登录：cookies 换了一批，seed 必须留着
		assert.NoError(t, c.SaveCookies([]byte(`[{"name":"web_session","value":"new"}]`)))

		assert.Equal(t, 23088, c.LoadSeed())
	})

	t.Run("文件损坏时降级为没有seed且不panic", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "cookies.json")
		assert.NoError(t, os.WriteFile(path, []byte(`{"version":2,"seed":`), 0644))

		assert.Equal(t, 0, NewLoadCookie(path).LoadSeed())
	})

	t.Run("文件不存在时读seed返回0", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "nope.json")

		assert.Equal(t, 0, NewLoadCookie(path).LoadSeed())
	})
}

// decodeJSON 把 JSON 解成通用结构，用于只比内容、不比排版。
func decodeJSON(t *testing.T, data []byte) any {
	t.Helper()

	var v any
	assert.NoError(t, json.Unmarshal(data, &v))
	return v
}

// TestSaveCookies_CreatesParentDir 保存时父目录不存在也应能落盘。
func TestSaveCookies_CreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deeper", "cookies.json")

	c := NewLoadCookie(path)
	assert.NoError(t, c.SaveCookies([]byte(`[{"name":"a","value":"b"}]`)))

	got, err := c.LoadCookies()
	assert.NoError(t, err)

	var cks []map[string]string
	assert.NoError(t, json.Unmarshal(got, &cks))
	assert.Equal(t, "a", cks[0]["name"])
}
