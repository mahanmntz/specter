(() => {
  'use strict';
  const PAGE = 50;
  const $ = (id) => document.getElementById(id);
  const state = { status: 'active', seniority: new Set(), keywords: new Set(), offset: 0, jobs: [], total: 0, facets: null };

  function el(tag, props = {}, ...kids) {
    const n = document.createElement(tag);
    for (const [k, v] of Object.entries(props)) {
      if (k === 'class') n.className = v;
      else if (k === 'text') n.textContent = v;
      else if (k.startsWith('on')) n.addEventListener(k.slice(2), v);
      else n.setAttribute(k, v);
    }
    for (const kid of kids) if (kid) n.append(kid);
    return n;
  }
  const safeUrl = (u) => { try { const x = new URL(u); return (x.protocol === 'https:' || x.protocol === 'http:') ? x.href : '#'; } catch { return '#'; } };
  const debounce = (fn, ms) => { let t; return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); }; };

  function ago(iso) {
    const d = new Date(iso); if (isNaN(d) || d.getFullYear() < 2000) return '';
    const h = (Date.now() - d) / 36e5;
    if (h < 1) return 'just now';
    if (h < 24) return Math.floor(h) + 'h ago';
    const days = Math.floor(h / 24);
    return days < 60 ? days + 'd ago' : Math.floor(days / 30) + 'mo ago';
  }

  function params(offset) {
    const p = new URLSearchParams();
    const set = (k, v) => { if (v) p.set(k, v); };
    set('q', $('q').value.trim());
    set('status', state.status === 'active' ? '' : state.status);
    if ($('global').checked) p.set('global', '1');
    if ($('contractor').checked) p.set('contractor', '1');
    if ($('fresh').value !== '0') p.set('fresh', $('fresh').value);
    if ($('min_score').value !== '0') p.set('min_score', $('min_score').value);
    set('company', $('company').value.trim());
    set('seniority', [...state.seniority].join(','));
    set('keyword', [...state.keywords].join(','));
    if ($('sort').value !== 'best') p.set('sort', $('sort').value);
    if (offset) { p.set('offset', offset); }
    return p;
  }

  function persist() {
    const p = params(0); const s = p.toString();
    try { history.replaceState(null, '', s ? '?' + s : location.pathname); } catch { /* ignore */ }
  }

  function restore() {
    const p = new URLSearchParams(location.search);
    $('q').value = p.get('q') || '';
    state.status = p.get('status') || 'active';
    $('global').checked = p.get('global') === '1';
    $('contractor').checked = p.get('contractor') === '1';
    $('fresh').value = p.get('fresh') || '0';
    $('min_score').value = p.get('min_score') || '0';
    $('company').value = p.get('company') || '';
    $('sort').value = p.get('sort') || 'best';
    (p.get('seniority') || '').split(',').filter(Boolean).forEach((s) => state.seniority.add(s));
    (p.get('keyword') || '').split(',').filter(Boolean).forEach((s) => state.keywords.add(s));
    document.querySelectorAll('#status-tabs button').forEach((b) => b.classList.toggle('on', b.dataset.status === state.status));
  }

  async function api(path, opts) {
    const r = await fetch(path, opts);
    if (!r.ok) throw new Error((await r.text()) || r.statusText);
    return r.json();
  }

  async function search(append = false) {
    if (!append) { state.offset = 0; persist(); }
    const p = params(state.offset); p.set('limit', PAGE);
    const data = await api('/api/jobs?' + p);
    state.total = data.total;
    state.jobs = append ? state.jobs.concat(data.jobs) : data.jobs;
    render();
  }

  function render() {
    const list = $('list');
    list.replaceChildren(...state.jobs.map(card));
    $('count').textContent = state.total + (state.total === 1 ? ' job' : ' jobs');
    $('more').hidden = state.jobs.length >= state.total;
    const empty = $('empty');
    empty.hidden = state.total !== 0;
    if (state.total === 0) {
      empty.replaceChildren(
        document.createTextNode(state.facets && state.facets.total === 0
          ? 'No jobs yet. Run a scan first: '
          : 'Nothing matches these filters.'),
        state.facets && state.facets.total === 0 ? el('code', { text: 'specter run' }) : '');
    }
  }

  async function setStatus(job, status) {
    const next = job.status === status ? '' : status;
    await api('/api/status', { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Specter': '1' }, body: JSON.stringify({ id: job.id, status: next }) });
    job.status = next;
    loadFacets();
    // Drop the card if it no longer belongs on the current tab.
    const visible = { active: next !== 'hidden', new: next === '', all: true }[state.status] ?? (next === state.status);
    if (!visible) { state.jobs = state.jobs.filter((j) => j !== job); state.total--; }
    render();
  }

  function card(job) {
    const isNew = job.first_seen && (Date.now() - new Date(job.first_seen)) < 3 * 864e5;
    const badges = el('div', { class: 'badges' });
    if (job.global_remote) badges.append(el('span', { class: 'badge good', text: '🌍 Worldwide' }));
    if (job.contractor_friendly) badges.append(el('span', { class: 'badge good', text: '🤝 Contractor' }));
    if (job.policy && !job.global_remote) badges.append(el('span', { class: 'badge', text: job.policy }));
    if (isNew) badges.append(el('span', { class: 'badge warn', text: 'NEW' }));
    if (job.seniority) badges.append(el('span', { class: 'badge', text: job.seniority }));
    if (job.compensation) badges.append(el('span', { class: 'badge', text: job.compensation }));
    if (job.score > 0) badges.append(el('span', { class: 'badge score', title: (job.matches || []).join(', '), text: job.score + '% match' }));

    const hits = new Set((job.matches || []).map((m) => m.toLowerCase()));
    const kw = el('div', { class: 'kw' }, ...(job.keywords || []).slice(0, 10).map((k) => el('span', { class: hits.has(k.toLowerCase()) ? 'hit' : '', text: k })));

    const toggle = (status, label, title) => el('button', {
      class: 'btn ' + (status === 'saved' || status === 'applied' ? '' : 'icon') + (job.status === status ? ' on' : ''),
      title, onclick: () => setStatus(job, status).catch(alert), text: label,
    });

    const when = ago(job.first_seen);
    const sub = [job.company, job.location, when && 'seen ' + when].filter(Boolean).join(' · ');

    return el('article', { class: 'card ' + (job.status || '') },
      el('div', { class: 'row' },
        el('div', {},
          el('a', { class: 'title', href: safeUrl(job.apply_url), target: '_blank', rel: 'noopener noreferrer', text: job.title }),
          el('div', { class: 'sub', text: sub }),
          badges, kw),
        el('div', { class: 'actions' },
          toggle('saved', job.status === 'saved' ? '★ Saved' : '☆ Save', 'Save for later'),
          toggle('applied', job.status === 'applied' ? '✓ Applied' : 'Mark applied', 'Mark as applied'),
          toggle('hidden', job.status === 'hidden' ? 'Unhide' : '✕', 'Hide this job'),
          el('a', { class: 'btn primary', href: safeUrl(job.apply_url), target: '_blank', rel: 'noopener noreferrer', text: 'Apply ↗' }))));
  }

  async function loadFacets() {
    const f = await api('/api/facets');
    state.facets = f;
    $('c-global').textContent = f.global;
    $('c-contractor').textContent = f.contractor;
    $('meta').textContent = f.total + ' jobs · ' + f.companies + ' companies' + (f.last_run ? ' · scanned ' + ago(f.last_run) : '');

    $('seniority').replaceChildren(...(f.seniority || []).map((s) => {
      const cb = el('input', { type: 'checkbox' }); cb.checked = state.seniority.has(s.name);
      cb.addEventListener('change', () => { cb.checked ? state.seniority.add(s.name) : state.seniority.delete(s.name); search(); });
      return el('label', { class: 'check' }, cb, document.createTextNode(s.name), el('span', { class: 'count', text: s.count }));
    }));
    $('keywords').replaceChildren(...(f.keywords || []).map((k) => el('button', {
      class: 'chip' + (state.keywords.has(k.name) ? ' on' : ''), text: k.name + ' ' + k.count,
      onclick: (e) => { state.keywords.has(k.name) ? state.keywords.delete(k.name) : state.keywords.add(k.name); e.currentTarget.classList.toggle('on'); search(); },
    })));
  }

  function reset() {
    $('q').value = ''; $('company').value = ''; $('fresh').value = '0'; $('min_score').value = '0'; $('sort').value = 'best';
    $('global').checked = false; $('contractor').checked = false;
    state.seniority.clear(); state.keywords.clear(); state.status = 'active';
    document.querySelectorAll('#status-tabs button').forEach((b) => b.classList.toggle('on', b.dataset.status === 'active'));
    loadFacets().then(() => search());
  }

  function init() {
    restore();
    const go = () => search().catch(showError);
    const goSoon = debounce(go, 200);
    $('q').addEventListener('input', goSoon);
    $('company').addEventListener('input', goSoon);
    ['global', 'contractor', 'fresh', 'min_score', 'sort'].forEach((id) => $(id).addEventListener('change', go));
    document.querySelectorAll('#status-tabs button').forEach((b) => b.addEventListener('click', () => {
      state.status = b.dataset.status;
      document.querySelectorAll('#status-tabs button').forEach((x) => x.classList.toggle('on', x === b));
      go();
    }));
    $('more').addEventListener('click', () => { state.offset = state.jobs.length; search(true).catch(showError); });
    $('reset').addEventListener('click', reset);
    document.addEventListener('keydown', (e) => {
      if (e.key === '/' && document.activeElement.tagName !== 'INPUT') { e.preventDefault(); $('q').focus(); }
    });
    loadFacets().then(go).catch(showError);
  }

  function showError(err) {
    $('empty').hidden = false;
    $('empty').textContent = 'Error: ' + err.message;
  }

  init();
})();
