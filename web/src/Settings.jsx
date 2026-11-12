import { Notice } from "./components.jsx";

export default function Settings({ workbench: wb }) {
  return (
    <>
      <header className="intro">
        <div>
          <p className="eyebrow">让创作工具，配合你的习惯</p>
          <h1 id="settings-title" tabIndex={-1}>
            设置
          </h1>
          <p className="muted">连接模型服务，管理当前浏览器的创作配置。</p>
        </div>
      </header>
      <form
        id="settings-form"
        className="card settings-form"
        onSubmit={(event) => {
          event.preventDefault();
          wb.saveSettings();
        }}
      >
        <div className="section-heading">
          <h2>模型与访问设置</h2>
        </div>
        <p className="muted settings-description">
          配置仅保存在当前浏览器。API Key
          会保存在浏览器本地存储中，请勿在共用设备保存；服务端不保存模型配置。
        </p>
        {["text", "image"].map((kind) => (
          <fieldset key={kind}>
            <legend>
              {kind === "text" ? (
                <>
                  文字模型 <span className="required">*</span>
                </>
              ) : (
                <>
                  生图模型 <span className="field-note">选填 · 独立配置</span>
                </>
              )}
            </legend>
            <label htmlFor={`${kind}-url`}>接口地址</label>
            <input
              type="url"
              id={`${kind}-url`}
              value={wb.settingsForm[kind].base_url}
              onChange={(event) =>
                wb.changeSetting(kind, "base_url", event.target.value)
              }
              placeholder={
                kind === "text"
                  ? "https://api.openai.com/v1"
                  : "支持图片生成的兼容接口地址"
              }
              disabled={wb.busy}
            />
            <label htmlFor={`${kind}-key`}>API Key</label>
            <input
              type="password"
              id={`${kind}-key`}
              autoComplete="off"
              value={wb.settingsForm[kind].api_key}
              onChange={(event) =>
                wb.changeSetting(kind, "api_key", event.target.value)
              }
              placeholder={`填写${kind === "text" ? "文字" : "生图"}模型的密钥`}
              disabled={wb.busy}
            />
            <label htmlFor={`${kind}-model`}>模型名称</label>
            <div className="input-action">
              <input
                id={`${kind}-model`}
                list={`${kind}-models`}
                value={wb.settingsForm[kind].model}
                onChange={(event) =>
                  wb.changeSetting(kind, "model", event.target.value)
                }
                placeholder="填写模型名称，或读取列表选择"
                disabled={wb.busy}
              />
              <button
                type="button"
                id={`${kind}-models-button`}
                className="button small"
                onClick={() => wb.readModels(kind)}
                disabled={wb.busy}
              >
                {wb.operation === `${kind}-models`
                  ? "正在读取…"
                  : "读取模型列表"}
              </button>
            </div>
            <datalist id={`${kind}-models`}>
              {wb.models[kind].map((model, index) => (
                <option key={`${model}-${index}`} value={model} />
              ))}
            </datalist>
          </fieldset>
        ))}
        <details className="advanced">
          <summary>高级：服务访问令牌</summary>
          <label htmlFor="service-token">工作台访问令牌</label>
          <input
            type="password"
            id="service-token"
            autoComplete="off"
            value={wb.settingsForm.token}
            onChange={(event) =>
              wb.changeSetting("token", "", event.target.value)
            }
            placeholder="仅在服务启用访问保护时填写"
            disabled={wb.busy}
          />
          <p className="helper">
            这是工作台服务的访问凭证，与模型 API Key 不同。
          </p>
        </details>
        <Notice id="settings-status" notice={wb.settingsStatus} />
        <div className="dialog-actions">
          <button
            type="button"
            id="clear-settings"
            className="button quiet"
            onClick={wb.clearSettings}
            disabled={wb.busy}
          >
            清除已存配置
          </button>
          <button type="submit" className="button primary" disabled={wb.busy}>
            保存设置
          </button>
        </div>
      </form>
    </>
  );
}
