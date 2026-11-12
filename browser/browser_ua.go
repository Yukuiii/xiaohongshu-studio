package browser

// UA 配置迁自 github.com/xpzouying/headless_browser v0.4.0，保留上游 MIT 许可。
//
// MIT License
//
// Copyright (c) 2025 zy
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

import (
	"strings"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// buildUAOverride 用真实浏览器版本补回 Rod 新页面的 UA 版本与 Chrome 品牌。
// 平台字段保持空值，交给内置引擎的源码补丁填充，避免与指纹平台矛盾。
func buildUAOverride(browser *rod.Browser, language string) (*proto.NetworkSetUserAgentOverride, error) {
	ver, err := proto.BrowserGetVersion{}.Call(browser)
	if err != nil {
		return nil, err
	}
	fullVersion := strings.TrimPrefix(ver.Product, "Chrome/")
	major := fullVersion
	if i := strings.IndexByte(fullVersion, '.'); i > 0 {
		major = fullVersion[:i]
	}
	ov := &proto.NetworkSetUserAgentOverride{
		UserAgent: ver.UserAgent,
		UserAgentMetadata: &proto.EmulationUserAgentMetadata{
			Brands: []*proto.EmulationUserAgentBrandVersion{
				{Brand: "Not:A-Brand", Version: "99"},
				{Brand: "Google Chrome", Version: major},
				{Brand: "Chromium", Version: major},
			},
			FullVersionList: []*proto.EmulationUserAgentBrandVersion{
				{Brand: "Not:A-Brand", Version: "99.0.0.0"},
				{Brand: "Google Chrome", Version: fullVersion},
				{Brand: "Chromium", Version: fullVersion},
			},
			FullVersion: fullVersion,
		},
	}
	if language != "" {
		primary := strings.SplitN(language, "-", 2)[0]
		ov.AcceptLanguage = language + "," + primary
	}
	return ov, nil
}
