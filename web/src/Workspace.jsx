import { Notice, Dialog } from "./components.jsx";
import { parseTags, sourceURL, titleLength } from "./useWorkbench.js";

export default function Workspace({ workbench: wb, account, openSettings }) {
  const { draft, busy, images, sources } = wb;
  const titleCount = titleLength(draft.title);
  const contentCount = Array.from(draft.content).length;
  const previewIndex = Math.min(
    wb.previewIndex,
    Math.max(0, images.length - 1),
  );
  const cover = images[previewIndex];
  const change = (key) => (event) => wb.changeDraft(key, event.target.value);
  const selectedCount = sources.filter((source) => source.selected).length;
  return (
    <>
      <header className="intro">
        <div>
          <p className="eyebrow">从一个灵感，到一篇好笔记</p>
          <h1 id="workspace-title" tabIndex={-1}>
            今天，想分享什么<span>？</span>
          </h1>
          <p className="muted">
            寻找真实参考，整理你的表达。每一次发布，都由你决定。
          </p>
        </div>
        <span className="desk-label">
          创作工作台 <span> / 01</span>
        </span>
      </header>
      {!(
        account.loggedIn &&
        wb.settings.text.base_url &&
        wb.settings.text.model
      ) && (
        <section className="onboarding" id="onboarding">
          <span className="onboarding-icon" aria-hidden="true">
            ✦
          </span>
          <div>
            <strong>先准备好你的创作台</strong>
            <p>
              在「设置」连接文字模型，再到「账号」添加小红书账号，即可搜索资料并生成草稿。配图也可以直接上传。
            </p>
          </div>
          <button
            type="button"
            className="button small"
            id="setup-start"
            disabled={busy}
            onClick={openSettings}
          >
            开始设置 →
          </button>
        </section>
      )}
      <ol className="steps" aria-label="创作进度">
        <li className="active">
          <span>01</span>寻找灵感
        </li>
        <li className={draft.title || draft.content ? "active" : ""}>
          <span>02</span>打磨笔记
        </li>
        <li
          className={
            draft.title && draft.content && images.length ? "active" : ""
          }
        >
          <span>03</span>确认发布
        </li>
      </ol>
      <Notice id="status" notice={wb.status} />
      {wb.uncertain && (
        <div id="publish-uncertain" className="notice warning">
          <strong>上次发布的结果尚未确认</strong>
          <p>
            请求可能已经发送成功。请先打开小红书账号核对笔记，避免重复发布。核对后可解除锁定。
          </p>
          <label className="check-label">
            <input
              type="checkbox"
              id="reviewed-publish"
              checked={wb.reviewedPublish}
              disabled={busy}
              onChange={(event) => wb.setReviewedPublish(event.target.checked)}
            />
            我已在小红书核对发布结果
          </label>
          <button
            type="button"
            id="unlock-publish"
            className="button small"
            disabled={busy || !wb.reviewedPublish}
            onClick={wb.unlockPublish}
          >
            解除发布锁定
          </button>
        </div>
      )}
      <div className="workspace-grid">
        <div className="editor-column">
          <section className="card" aria-labelledby="research-title">
            <div className="section-heading">
              <div>
                <span className="section-kicker">01 / RESEARCH</span>
                <h2 id="research-title">给灵感一个起点</h2>
              </div>
              <span className="pill">真实笔记参考</span>
            </div>
            <form
              id="research-form"
              onSubmit={(event) => {
                event.preventDefault();
                wb.research(true, account.requireLogin);
              }}
            >
              <label htmlFor="topic">
                创作选题 <span className="required">*</span>
              </label>
              <input
                id="topic"
                value={draft.topic}
                onChange={change("topic")}
                placeholder="例如：适合上班族的 15 分钟早餐"
                required
                maxLength={500}
                disabled={busy}
              />
              <label htmlFor="keywords">
                搜索关键词{" "}
                <span className="field-note">最多 3 个，用逗号或换行分隔</span>
              </label>
              <textarea
                id="keywords"
                rows={2}
                value={draft.keywords}
                onChange={change("keywords")}
                placeholder="快手早餐，健康早餐，上班族早餐"
                disabled={busy}
              />
              <label htmlFor="instructions">
                你希望怎么写 <span className="field-note">选填</span>
              </label>
              <textarea
                id="instructions"
                rows={3}
                value={draft.instructions}
                onChange={change("instructions")}
                placeholder="例如：语气自然、步骤清楚，面向厨房新手；不要编造亲身体验。"
                disabled={busy}
              />
              <div className="form-bottom">
                <span className="helper">搜索参考 → 读取正文 → 整理草稿</span>
                <div className="button-group">
                  {wb.researching && (
                    <button
                      type="button"
                      id="cancel-research"
                      className="button quiet"
                      onClick={wb.cancelResearch}
                    >
                      取消
                    </button>
                  )}
                  <button
                    id="research-button"
                    type="submit"
                    className="button primary"
                    disabled={busy}
                  >
                    {wb.operation === "research" ? (
                      "正在搜索与写稿…"
                    ) : (
                      <>
                        搜索并生成草稿 <span aria-hidden="true">↗</span>
                      </>
                    )}
                  </button>
                </div>
              </div>
            </form>
            <div className="sources-section">
              <div className="subheading">
                <h3>
                  参考资料{" "}
                  <span id="source-count" className="count">
                    {selectedCount} / {sources.length}
                  </span>
                </h3>
                <button
                  type="button"
                  id="regenerate"
                  className="button text-button"
                  disabled={busy || !selectedCount}
                  onClick={() => wb.research(false, account.requireLogin)}
                >
                  {wb.operation === "regenerate"
                    ? "正在重新生成…"
                    : "按所选资料重新生成 ↗"}
                </button>
              </div>
              <p className="helper">
                默认选取前 3 篇，可调整为 1–5
                篇。只有成功读取正文的资料才会用于写稿。
              </p>
              <div id="source-list" className="source-list">
                {!sources.length && (
                  <div className="empty-state">
                    <span className="empty-symbol" aria-hidden="true">
                      ⌕
                    </span>
                    <p>好内容，从了解开始</p>
                    <small>搜索后，你的参考笔记会出现在这里。</small>
                  </div>
                )}
                {sources.map((source, index) => (
                  <div
                    key={source.id}
                    className={`source-item${source.selected ? " selected" : ""}`}
                  >
                    <input
                      type="checkbox"
                      id={`source-${index}`}
                      checked={source.selected}
                      disabled={busy || !source.token}
                      onChange={(event) =>
                        wb.selectSource(source.id, event.target.checked)
                      }
                    />
                    <div className="source-info">
                      <label htmlFor={`source-${index}`}>
                        {source.title || "未命名笔记"}
                      </label>
                      <div className="source-meta">
                        <span>{source.author || "小红书创作者"}</span>
                        <span>
                          {source.used
                            ? "已用于当前草稿"
                            : source.content
                              ? "正文已读取"
                              : source.token
                                ? "待读取正文"
                                : "缺少访问凭证，无法选取"}
                        </span>
                        {source.token && (
                          <a
                            href={sourceURL(source)}
                            target="_blank"
                            rel="noopener noreferrer"
                          >
                            查看原笔记 ↗
                          </a>
                        )}
                      </div>
                      {source.content && (
                        <p className="source-excerpt">{source.content}</p>
                      )}
                      {source.error && (
                        <p className="source-error">{source.error}</p>
                      )}
                    </div>
                  </div>
                ))}
              </div>
            </div>
          </section>
          <section className="card" aria-labelledby="draft-title">
            <div className="section-heading">
              <div>
                <span className="section-kicker">02 / EDIT</span>
                <h2 id="draft-title">把表达，变成你的</h2>
              </div>
              <button
                type="button"
                id="save-draft"
                className="button quiet small"
                disabled={busy}
                onClick={wb.save}
              >
                保存草稿
              </button>
            </div>
            <div className="label-row">
              <label htmlFor="title">笔记标题</label>
              <span
                id="title-count"
                className={`counter${titleCount > 20 ? " over" : ""}`}
              >
                {titleCount} / 20
              </span>
            </div>
            <input
              id="title"
              value={draft.title}
              onChange={change("title")}
              placeholder="一个让人想点开的标题"
              disabled={busy}
            />
            <div className="label-row">
              <label htmlFor="content">正文</label>
              <span
                id="content-count"
                className={`counter${contentCount > 1000 ? " over" : ""}`}
              >
                {contentCount} / 1000
              </span>
            </div>
            <textarea
              id="content"
              className="article-input"
              rows={12}
              value={draft.content}
              onChange={change("content")}
              placeholder={
                "把你的发现、观点和实用建议写在这里。\n\n生成后仍可自由编辑；发布前请核对事实，加入自己的表达。"
              }
              disabled={busy}
            />
            <label htmlFor="tags">
              话题标签 <span className="field-note">用逗号、空格或 # 分隔</span>
            </label>
            <input
              id="tags"
              value={draft.tags}
              onChange={change("tags")}
              placeholder="#早餐灵感 #认真吃早餐"
              disabled={busy}
            />
            <div className="editor-footnote">
              <span aria-hidden="true">✧</span> AI
              协助表达，你负责判断。请确认事实、原创性与图片使用权。
            </div>
          </section>
          <section className="card" aria-labelledby="images-title">
            <div className="section-heading">
              <div>
                <span className="section-kicker">03 / VISUALS</span>
                <h2 id="images-title">让笔记有画面</h2>
              </div>
              <span id="image-count" className="pill">
                {images.length} / 9 张
              </span>
            </div>
            <div id="image-list" className="image-grid">
              {images.map((image, index) => (
                <div key={image.id} className="image-tile">
                  {image.blobURL ? (
                    <img
                      src={image.blobURL}
                      alt={image.name || `配图 ${index + 1}`}
                    />
                  ) : (
                    <div className="image-error">
                      {image.error || "正在读取图片…"}
                    </div>
                  )}
                  <button
                    type="button"
                    className="image-remove"
                    aria-label={`移除第 ${index + 1} 张配图`}
                    disabled={busy}
                    onClick={() => wb.removeImage(image.id)}
                  >
                    ×
                  </button>
                  <span className="image-badge">
                    {index === 0 ? "封面" : index + 1}
                  </span>
                </div>
              ))}
            </div>
            <label
              htmlFor="upload"
              className={`upload-zone${busy || images.length >= 9 ? " busy" : ""}`}
              id="upload-zone"
            >
              <span className="upload-plus">+</span>
              <strong>选择本地图片</strong>
              <span>
                JPEG / PNG / WebP / GIF · 每张不超过 10 MB · 最多 9 张
              </span>
              <input
                type="file"
                id="upload"
                accept="image/jpeg,image/png,image/webp,image/gif"
                multiple
                disabled={busy || images.length >= 9}
                onChange={(event) => {
                  const files = [...event.target.files];
                  event.target.value = "";
                  wb.upload(files);
                }}
              />
            </label>
            <details className="image-ai">
              <summary>
                用 AI 为这篇笔记生成配图 <span>可选</span>
              </summary>
              <div>
                <label htmlFor="image-prompt">画面描述</label>
                <textarea
                  id="image-prompt"
                  rows={3}
                  value={draft["image-prompt"]}
                  onChange={change("image-prompt")}
                  placeholder="描述画面主体、风格、色彩和构图；可在草稿生成后继续调整。"
                  disabled={busy}
                />
                <div className="form-bottom">
                  <span className="helper">需在设置中单独配置生图模型</span>
                  <button
                    type="button"
                    id="generate-image"
                    className="button"
                    disabled={busy || images.length >= 9}
                    onClick={wb.generateImage}
                  >
                    {wb.operation === "generate-image"
                      ? "正在生成配图…"
                      : "生成一张配图 ✦"}
                  </button>
                </div>
              </div>
            </details>
          </section>
        </div>
        <aside className="preview-column" aria-label="笔记预览与发布">
          <div className="preview-sticky">
            <div className="preview-heading">
              <span className="section-kicker">LIVE PREVIEW</span>
              <span>
                <span className="live-dot"></span> 实时预览
              </span>
            </div>
            <div className="phone">
              <div className="phone-header">
                <span aria-hidden="true">‹</span>
                <span className="avatar">薯</span>
                <span id="preview-author">
                  {account.loggedIn ? account.username : "你的创作账号"}
                </span>
                <span className="preview-follow">创作中</span>
              </div>
              <div className="preview-cover" id="preview-cover">
                {cover ? (
                  cover.blobURL ? (
                    <img
                      src={cover.blobURL}
                      alt={`笔记配图 ${previewIndex + 1}`}
                    />
                  ) : (
                    <div className="image-error">
                      {cover.error || "正在读取配图…"}
                    </div>
                  )
                ) : (
                  <div className="preview-placeholder">
                    <span>
                      把生活的灵感
                      <br />
                      装进一篇笔记
                    </span>
                    <small>上传或生成图片，即可预览封面</small>
                  </div>
                )}
                {images.length > 1 && (
                  <div className="cover-controls">
                    <button
                      type="button"
                      aria-label="预览上一张配图"
                      onClick={() => wb.movePreview(-1)}
                    >
                      ‹
                    </button>
                    <span>
                      {previewIndex + 1} / {images.length}
                    </span>
                    <button
                      type="button"
                      aria-label="预览下一张配图"
                      onClick={() => wb.movePreview(1)}
                    >
                      ›
                    </button>
                  </div>
                )}
              </div>
              <div className="preview-body">
                <h3 id="preview-title">{draft.title || "你的下一篇好笔记"}</h3>
                <p id="preview-content">
                  {draft.content ||
                    "从左侧的一个选题开始，或者直接写下你想分享的内容。"}
                </p>
                <p id="preview-tags" className="preview-tags">
                  {parseTags(draft.tags)
                    .map((tag) => `#${tag}`)
                    .join(" ")}
                </p>
                <div className="preview-date">刚刚 · 待发布</div>
              </div>
              <div className="phone-footer">
                <span>说点什么…</span>
                <span aria-hidden="true">♡　☆　♧</span>
              </div>
            </div>
            <p className="preview-disclaimer">
              预览仅供排版参考，实际展示以小红书为准
            </p>
            <section className="publish-panel">
              <label htmlFor="visibility">谁可以看</label>
              <select
                id="visibility"
                value={draft.visibility}
                onChange={change("visibility")}
                disabled={busy}
              >
                {["公开可见", "仅自己可见", "仅互关好友可见"].map((value) => (
                  <option key={value}>{value}</option>
                ))}
              </select>
              <button
                type="button"
                id="publish-open"
                className="button primary publish-button"
                disabled={busy}
                onClick={() => wb.preparePublish(account.requireLogin)}
              >
                {wb.operation === "publish-check" ? (
                  "正在检查账号…"
                ) : (
                  <>
                    检查并发布 <span aria-hidden="true">→</span>
                  </>
                )}
              </button>
              <p>先确认，再发布。不会自动发布任何内容。</p>
              {wb.publishValidation && (
                <div
                  id="publish-validation"
                  className="inline-error"
                  role="status"
                >
                  {wb.publishValidation}
                </div>
              )}
            </section>
          </div>
        </aside>
      </div>
      <footer className="page-footer">
        <span className="brand-mini">红薯创作台</span>
        <span>让工具处理繁琐，让你专注表达。</span>
        <button
          type="button"
          id="clear-draft"
          className="button text-button"
          disabled={busy}
          onClick={() => wb.showClearDialog(true)}
        >
          清空本地草稿
        </button>
      </footer>
      <Dialog
        open={Boolean(wb.confirmation)}
        onClose={wb.closePublish}
        busy={busy}
        id="publish-dialog"
        title="准备好分享了吗？"
      >
        <p className="muted">
          点击「确认发布」会将这篇笔记真实发布到当前小红书账号。请最后核对内容与可见范围。
        </p>
        <dl className="publish-summary">
          <dt>发布账号</dt>
          <dd id="confirm-account">{wb.confirmation?.username}</dd>
          <dt>笔记标题</dt>
          <dd id="confirm-title">{wb.confirmation?.title}</dd>
          <dt>图片数量</dt>
          <dd id="confirm-images">{wb.confirmation?.images.length} 张</dd>
          <dt>可见范围</dt>
          <dd id="confirm-visibility">{wb.confirmation?.visibility}</dd>
        </dl>
        <label className="check-label">
          <input
            type="checkbox"
            id="publish-agree"
            checked={wb.publishAgreed}
            disabled={busy}
            onChange={(event) => wb.setPublishAgreed(event.target.checked)}
          />
          我已核对正文、配图及事实，确认发布这篇笔记
        </label>
        <Notice id="publish-status" notice={wb.publishStatus} />
        <div className="dialog-actions">
          <button
            type="button"
            className="button"
            disabled={busy}
            onClick={wb.closePublish}
          >
            返回编辑
          </button>
          <button
            type="button"
            id="publish-confirm"
            className="button primary"
            disabled={busy || !wb.publishAgreed}
            onClick={wb.confirmPublish}
          >
            {wb.operation === "publish-confirm"
              ? "正在发布，请勿关闭…"
              : "确认发布"}
          </button>
        </div>
      </Dialog>
      <Dialog
        open={wb.clearDialog}
        onClose={() => wb.showClearDialog(false)}
        busy={busy}
        id="clear-dialog"
        title="清空当前草稿？"
      >
        <p className="muted">
          将清除当前浏览器的文字、参考资料及图片引用。服务端已保存的图片不会被删除，创作设置也会保留。
        </p>
        <div className="dialog-actions">
          <button
            type="button"
            className="button"
            onClick={() => wb.showClearDialog(false)}
            disabled={busy}
          >
            保留草稿
          </button>
          <button
            type="button"
            className="button primary"
            id="clear-draft-confirm"
            onClick={wb.clearDraft}
            disabled={busy}
          >
            确认清空
          </button>
        </div>
      </Dialog>
    </>
  );
}
