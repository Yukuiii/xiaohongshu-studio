import { useEffect, useState } from "react";
import { Dialog, Notice } from "./components.jsx";

export default function Settings({ workbench: wb, active = true }) {
  const [confirmClear, setConfirmClear] = useState(false);
  useEffect(() => {
    if (!active) setConfirmClear(false);
  }, [active]);
  return (
    <>
      <header className="intro">
        <div>
          <p className="eyebrow">让创作工具，配合你的习惯</p>
          <h1 id="settings-title" tabIndex={-1}>
            设置
          </h1>
          <p className="muted">连接模型服务，管理工作台共用的模型配置。</p>
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
          模型配置保存在服务端，所有账号共用。Docker
          部署会保存到数据卷，换浏览器或重建容器后可继续使用。已保存的 API Key
          不会返回浏览器。
        </p>
        {wb.settingsLoading && <p role="status">正在读取服务端模型配置…</p>}
        {["text", "image"].map((kind) => (
          <fieldset key={kind} disabled={wb.busy || wb.settingsLoading}>
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
              placeholder={
                wb.settingsForm[kind].has_api_key
                  ? "服务端已保存，留空保留原密钥"
                  : `填写${kind === "text" ? "文字" : "生图"}模型的密钥`
              }
              disabled={wb.busy}
            />
            {wb.settingsForm[kind].has_api_key && (
              <p className="helper">
                同一接口可沿用已保存的密钥；更换接口地址时请填写新的 API Key。
              </p>
            )}
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
            onClick={() => setConfirmClear(true)}
            disabled={wb.busy || wb.settingsLoading || !wb.settingsReady}
          >
            清空模型配置
          </button>
          <button type="submit" className="button primary" disabled={wb.busy}>
            保存设置
          </button>
        </div>
      </form>
      <Dialog
        open={active && confirmClear}
        onClose={() => setConfirmClear(false)}
        busy={wb.busy}
        id="clear-model-settings"
        title="清空服务端模型配置？"
      >
        <p className="muted">
          将删除文字和生图模型的地址、名称及 API
          Key，所有使用此工作台的浏览器都会受到影响。账号、草稿和本浏览器的服务访问令牌会保留。
        </p>
        <Notice
          notice={wb.settingsStatus.type === "error" ? wb.settingsStatus : null}
        />
        <div className="dialog-actions">
          <button
            type="button"
            className="button"
            disabled={wb.busy}
            onClick={() => setConfirmClear(false)}
          >
            取消
          </button>
          <button
            type="button"
            className="button primary"
            disabled={wb.busy}
            onClick={async () => {
              if (await wb.clearSettings()) setConfirmClear(false);
            }}
          >
            确认清空
          </button>
        </div>
      </Dialog>
    </>
  );
}
