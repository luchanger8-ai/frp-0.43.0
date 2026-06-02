// Copyright 2021 The frp Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

func ParseClientConfig(filePath string) (
	cfg ClientCommonConf,
	pxyCfgs map[string]ProxyConf,
	visitorCfgs map[string]VisitorConf,
	err error,
) {
	// 调用入口：cmd/frpc/sub/root.go 读取 frpc.ini 时调用。
	// 解析结果会传入 client/service.go:NewService()，作为客户端启动、代理注册和 visitor 管理的配置来源。
	var content []byte
	content, err = GetRenderedConfFromFile(filePath)
	if err != nil {
		return
	}
	configBuffer := bytes.NewBuffer(nil)
	configBuffer.Write(content)

	// Parse common section.
	// common 段解析为 ClientCommonConf，字段定义见 pkg/config/client.go。
	cfg, err = UnmarshalClientConfFromIni(content)
	if err != nil {
		return
	}
	cfg.Complete()
	if err = cfg.Validate(); err != nil {
		err = fmt.Errorf("Parse config error: %v", err)
		return
	}

	// Aggregate proxy configs from include files.
	// includes 会把额外配置文件拼接进来，再统一解析代理和 visitor。
	var buf []byte
	buf, err = getIncludeContents(cfg.IncludeConfigFiles)
	if err != nil {
		err = fmt.Errorf("getIncludeContents error: %v", err)
		return
	}
	configBuffer.WriteString("\n")
	configBuffer.Write(buf)

	// Parse all proxy and visitor configs.
	// 代理配置类型定义见 pkg/config/proxy.go；visitor 配置类型定义见 pkg/config/visitor.go。
	pxyCfgs, visitorCfgs, err = LoadAllProxyConfsFromIni(cfg.User, configBuffer.Bytes(), cfg.Start)
	if err != nil {
		return
	}
	return
}

// getIncludeContents renders all configs from paths.
// files format can be a single file path or directory or regex path.
// 调用位置：pkg/config/parse.go:ParseClientConfig()。
// 作用：读取 includes 指定的额外配置文件，支持通配路径匹配。
func getIncludeContents(paths []string) ([]byte, error) {
	out := bytes.NewBuffer(nil)
	for _, path := range paths {
		absDir, err := filepath.Abs(filepath.Dir(path))
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(absDir); os.IsNotExist(err) {
			return nil, err
		}
		files, err := os.ReadDir(absDir)
		if err != nil {
			return nil, err
		}
		for _, fi := range files {
			if fi.IsDir() {
				continue
			}
			absFile := filepath.Join(absDir, fi.Name())
			if matched, _ := filepath.Match(filepath.Join(absDir, filepath.Base(path)), absFile); matched {
				tmpContent, err := GetRenderedConfFromFile(absFile)
				if err != nil {
					return nil, fmt.Errorf("render extra config %s error: %v", absFile, err)
				}
				out.Write(tmpContent)
				out.WriteString("\n")
			}
		}
	}
	return out.Bytes(), nil
}
