// Logs: tail asrd/smbd/console logs with source, level and line-count filters.

import { api, errorMessage } from '../api.js';
import { el, field, spinner } from '../ui.js';

const SOURCES = [
  { value: 'asrd', label: 'asrd（守护进程）' },
  { value: 'smbd', label: 'smbd' },
  { value: 'console', label: 'smbd 控制台' },
];
const LEVELS = [
  { value: '', label: '全部级别' },
  { value: 'debug', label: '调试及以上' },
  { value: 'info', label: '信息及以上' },
  { value: 'warn', label: '警告及以上' },
  { value: 'error', label: '仅错误' },
];
const LIMITS = ['50', '100', '200', '500', '1000'];
const LEVEL_RE = /\[(TRACE|DEBUG|INFO|WARN|WARNING|ERROR|FATAL)\]/;

function lineClass(line) {
  const match = LEVEL_RE.exec(String(line));
  if (!match) return 'log-line';
  const level = match[1].toLowerCase();
  if (level === 'warn' || level === 'warning') return 'log-line log-warn';
  if (level === 'error' || level === 'fatal') return 'log-line log-error';
  if (level === 'debug' || level === 'trace') return 'log-line log-debug';
  return 'log-line log-info';
}

export async function mount(root) {
  let lines = [];
  let loading = false;

  const sourceSelect = el('select', { id: 'log-source' });
  for (const source of SOURCES) {
    sourceSelect.appendChild(el('option', { value: source.value, text: source.label }));
  }
  sourceSelect.value = 'asrd';

  const levelSelect = el('select', { id: 'log-level' });
  for (const level of LEVELS) {
    levelSelect.appendChild(el('option', { value: level.value, text: level.label }));
  }
  levelSelect.value = '';

  const limitSelect = el('select', { id: 'log-limit' });
  for (const limit of LIMITS) {
    limitSelect.appendChild(el('option', { value: limit, text: `${limit} 行` }));
  }
  limitSelect.value = '200';

  const refreshBtn = el('button', {
    class: 'btn btn-primary',
    type: 'button',
    text: '刷新',
    onclick: () => load(),
  });

  const errorBox = el('div', {
    class: 'notice notice-error',
    role: 'alert',
    hidden: true,
  });
  const meta = el('div', { class: 'log-meta' });
  const logView = el('div', {
    class: 'log-view',
    role: 'log',
    'aria-label': '日志输出',
    tabindex: '0',
  });

  root.append(
    el(
      'div',
      { class: 'log-controls' },
      field({ id: 'log-source', label: '来源', control: sourceSelect }),
      field({ id: 'log-level', label: '最低级别', control: levelSelect }),
      field({ id: 'log-limit', label: '行数', control: limitSelect }),
      refreshBtn,
    ),
    errorBox,
    meta,
    logView,
  );

  for (const control of [sourceSelect, levelSelect, limitSelect]) {
    control.addEventListener('change', () => load());
  }

  function render() {
    const sourceLabel =
      (SOURCES.find((source) => source.value === sourceSelect.value) || {}).label ||
      sourceSelect.value;
    meta.replaceChildren(
      el('span', { text: `${lines.length} 行 · ${sourceLabel}` }),
      el('span', {
        class: 'muted',
        text: `更新于 ${new Date().toLocaleTimeString()}`,
      }),
    );
    if (lines.length === 0) {
      logView.replaceChildren(
        el('div', {
          class: 'log-empty',
          text: '该来源和级别下没有日志。',
        }),
      );
      return;
    }
    logView.replaceChildren(
      ...lines.map((line) => el('div', { class: lineClass(line), text: String(line) })),
    );
    logView.scrollTop = logView.scrollHeight;
  }

  async function load() {
    if (loading) return;
    loading = true;
    refreshBtn.disabled = true;
    errorBox.hidden = true;
    logView.replaceChildren(
      el('div', { class: 'loading-inline' }, spinner(), el('span', { text: '正在加载日志…' })),
    );
    try {
      const result = await api.logs(
        sourceSelect.value,
        levelSelect.value,
        Number(limitSelect.value),
      );
      lines = result && Array.isArray(result.lines) ? result.lines : [];
      render();
    } catch (error) {
      if (error && error.status === 401) return;
      lines = [];
      logView.replaceChildren(
        el('div', { class: 'log-empty', text: '没有可用的日志输出。' }),
      );
      meta.replaceChildren(el('span', { class: 'muted', text: '日志请求失败' }));
      errorBox.replaceChildren(
        el('span', { text: errorMessage(error) }),
        error && error.status === 400
          ? el('span', {
              class: 'hint',
              text: ' 相关服务启动后才会生成日志文件。',
            })
          : null,
      );
      errorBox.hidden = false;
    } finally {
      loading = false;
      refreshBtn.disabled = false;
    }
  }

  await load();
}
