(() => {
  'use strict';
  const PAGE = 40;
  const LEVELS = [['Junior', 'Junior'], ['Mid/Standard', 'Mid'], ['Senior', 'Senior'], ['Staff/Principal', 'Staff+'], ['Lead/Engineering Manager', 'Lead']];
  const ICONS = {
    system: '<svg viewBox="0 0 24 24"><rect x="3" y="4" width="18" height="12" rx="2"/><path d="M8 20h8M12 16v4"/></svg>',
    light: '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/></svg>',
    dark: '<svg viewBox="0 0 24 24"><path d="M21 12.8A9 9 0 1 1 11.2 3 7 7 0 0 0 21 12.8Z"/></svg>',
  };
  const $ = (id) => document.getElementById(id);
  const state = {
    status: 'active', levels: new Set(), keywords: new Set(), offset: 0, jobs: [], total: 0,
    facets: null, profile: null, run: { state: 'idle' }, wasRunning: false, pollTimer: null, sel: -1, drawerJob: null,
    view: 'list', pipeline: null,
  };
  const STAGES = [['saved', 'Saved'], ['applied', 'Applied'], ['interview', 'Interview'], ['offer', 'Offer'], ['rejected', 'Rejected']];

  // ---------- helpers ----------
  function el(tag, props = {}, ...kids) {
    const n = document.createElement(tag);
    for (const [k, v] of Object.entries(props)) {
      if (v === undefined || v === null || v === false) continue;
      if (k === 'class') n.className = v;
      else if (k === 'text') n.textContent = v;
      else if (k.startsWith('on')) n.addEventListener(k.slice(2), v);
      else n.setAttribute(k, v === true ? '' : v);
    }
    for (const kid of kids) if (kid !== null && kid !== undefined && kid !== false) n.append(kid);
    return n;
  }
  const safeUrl = (u) => { try { const x = new URL(u); return /^https?:$/.test(x.protocol) ? x.href : '#'; } catch { return '#'; } };
  const debounce = (fn, ms) => { let t; return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); }; };
  const hue = (s) => { let h = 0; for (const c of s) h = (h * 31 + c.charCodeAt(0)) % 360; return h; };
  const plural = (n, w) => n + ' ' + w + (n === 1 ? '' : 's');
  const lc = (s) => String(s).toLowerCase();
  const store = {
    get: (k, d) => { try { return localStorage.getItem(k) || d; } catch { return d; } },
    set: (k, v) => { try { v === null ? localStorage.removeItem(k) : localStorage.setItem(k, v); } catch { /* ignore */ } },
  };
  // replaceChildren() stringifies null/false, so drop empty slots first.
  const fill = (node, ...kids) => node.replaceChildren(...kids.flat().filter((k) => k !== null && k !== undefined && k !== false));
  const svg = (markup) => { const s = el('span', { class: 'ico' }); s.innerHTML = markup; return s; };

  function ago(iso) {
    const d = new Date(iso); if (isNaN(d) || d.getFullYear() < 2000) return '';
    const h = (Date.now() - d) / 36e5;
    if (h < 1) return 'just now';
    if (h < 24) return Math.floor(h) + 'h ago';
    const days = Math.floor(h / 24);
    return days < 45 ? days + 'd ago' : Math.floor(days / 30) + 'mo ago';
  }

  async function api(path, opts = {}) {
    const headers = { ...(opts.headers || {}) };
    if (opts.method && opts.method !== 'GET') { headers['Content-Type'] = 'application/json'; headers['X-Specter'] = '1'; }
    const r = await fetch(path, { ...opts, headers });
    const text = await r.text();
    let data = null; try { data = JSON.parse(text); } catch { /* not json */ }
    if (!r.ok) throw new Error((data && data.error) || text || r.statusText);
    return data;
  }

  let toastTimer;
  function toast(msg, ms = 3500) {
    const t = $('toast'); t.textContent = msg; t.hidden = false;
    clearTimeout(toastTimer); toastTimer = setTimeout(() => { t.hidden = true; }, ms);
  }

  // ---------- theme ----------
  const themeNow = () => document.documentElement.getAttribute('data-theme') || 'system';
  function renderTheme() {
    const t = themeNow(); const b = $('theme');
    b.innerHTML = ICONS[t];
    b.title = 'Theme: ' + t + ' (click to change)';
    b.setAttribute('aria-label', 'Theme: ' + t);
  }
  function cycleTheme() {
    const next = { system: 'light', light: 'dark', dark: 'system' }[themeNow()];
    if (next === 'system') { document.documentElement.removeAttribute('data-theme'); store.set('theme', null); }
    else { document.documentElement.setAttribute('data-theme', next); store.set('theme', next); }
    renderTheme();
  }

  // ---------- filters / search ----------
  const isOn = (id) => $(id).getAttribute('aria-pressed') === 'true';
  const pressed = (id, on) => $(id).setAttribute('aria-pressed', on ? 'true' : 'false');

  function params(offset) {
    const p = new URLSearchParams();
    const q = $('q').value.trim(); if (q) p.set('q', q);
    if (state.status !== 'active') p.set('status', state.status);
    if (isOn('global')) p.set('global', '1');
    if (isOn('contractor')) p.set('contractor', '1');
    if ($('fresh').value !== '0') p.set('fresh', $('fresh').value);
    if ($('min_score').value !== '0') p.set('min_score', $('min_score').value);
    const c = $('company').value.trim(); if (c) p.set('company', c);
    if (state.levels.size) p.set('seniority', [...state.levels].join(','));
    if (state.keywords.size) p.set('keyword', [...state.keywords].join(','));
    if ($('sort').value !== 'best') p.set('sort', $('sort').value);
    if (offset) p.set('offset', offset);
    return p;
  }
  const hasFilters = () => { const p = params(0); p.delete('status'); p.delete('sort'); return [...p.keys()].length > 0; };

  function persistURL() {
    const p = params(0); if (state.view === 'board') p.set('view', 'board');
    const s = p.toString();
    try { history.replaceState(null, '', s ? '?' + s : location.pathname); } catch { /* ignore */ }
    $('reset').hidden = !hasFilters();
    renderStats();
  }

  function restoreFromURL() {
    const p = new URLSearchParams(location.search);
    $('q').value = p.get('q') || '';
    state.status = p.get('status') || 'active';
    pressed('global', p.get('global') === '1');
    pressed('contractor', p.get('contractor') === '1');
    $('fresh').value = p.get('fresh') || '0';
    $('min_score').value = p.get('min_score') || '0';
    $('company').value = p.get('company') || '';
    $('sort').value = p.get('sort') || 'best';
    state.levels = new Set((p.get('seniority') || '').split(',').filter(Boolean));
    state.keywords = new Set((p.get('keyword') || '').split(',').filter(Boolean));
    syncTabs();
    return [...p.keys()].length > 0;
  }

  function syncTabs() {
    const st = (state.facets && state.facets.statuses) || {};
    document.querySelectorAll('#tabs button').forEach((b) => {
      b.classList.toggle('on', b.dataset.status === state.status);
      const n = st[b.dataset.status];
      b.querySelector('.n') && b.querySelector('.n').remove();
      if (n) b.append(el('span', { class: 'n', text: n }));
    });
  }

  async function search(append = false) {
    if (!append) { state.offset = 0; state.sel = -1; persistURL(); }
    const p = params(state.offset); p.set('limit', PAGE);
    const data = await api('/api/jobs?' + p);
    state.total = data.total;
    state.jobs = append ? state.jobs.concat(data.jobs) : data.jobs;
    render();
  }

  // ---------- hero + stats ----------
  function renderHero() {
    const pr = state.profile; const f = state.facets;
    const hero = $('hero');
    if (!pr || !pr.configured || !f) { hero.hidden = true; return; }
    hero.hidden = false;
    const first = (pr.prefs.name || '').trim().split(/\s+/)[0];
    const h = new Date().getHours();
    const hello = h < 5 ? 'Still up' : h < 12 ? 'Good morning' : h < 18 ? 'Good afternoon' : 'Good evening';
    const preset = pr.presets.find((p) => p.id === pr.prefs.role);
    $('greeting').textContent = first ? hello + ', ' + first : 'Your jobs';
    const visible = f.total - ((f.statuses && f.statuses.hidden) || 0);
    $('subgreeting').textContent = visible
      ? plural(visible, 'role') + ' for your ' + (preset ? preset.label : '') + ' profile' + (f.last_run && ago(f.last_run) ? ' · updated ' + ago(f.last_run) : '')
      : 'Press “Find jobs” to search for roles that fit you.';
    renderStats();
  }

  function renderStats() {
    const f = state.facets; if (!f || $('hero').hidden) return;
    const st = f.statuses || {};
    const visible = f.total - (st.hidden || 0);
    const card = (num, label, on, onclick) => el('button', { class: 'stat' + (on ? ' on' : ''), onclick }, el('b', { text: String(num) }), el('span', { text: label }));
    $('stats').replaceChildren(
      card(visible, 'Matching roles', false, resetFilters),
      card(f.global, '🌍 Worldwide', isOn('global') && state.status === 'active', () => { pressed('global', !isOn('global')); search().catch(showError); }),
      card(f.new_week || 0, 'New this week', $('fresh').value === '7', () => { $('fresh').value = $('fresh').value === '7' ? '0' : '7'; search().catch(showError); }),
      card(st.saved || 0, '★ Saved', state.status === 'saved', () => { state.status = state.status === 'saved' ? 'active' : 'saved'; syncTabs(); search().catch(showError); }));
  }

  // ---------- rendering ----------
  function render() {
    $('list').replaceChildren(...state.jobs.map(row));
    $('count').textContent = state.total ? plural(state.total, 'job') : '';
    $('loadmore').hidden = state.jobs.length >= state.total;
    syncTabs();

    const empty = $('empty'); empty.hidden = state.total !== 0;
    if (state.total === 0) {
      const noData = state.facets && state.facets.total === 0;
      const canRun = state.profile && state.profile.can_run && state.profile.configured;
      fill(empty,
        el('h2', { text: noData ? 'No jobs yet' : state.status !== 'active' ? 'Nothing here yet' : 'No matches' }),
        el('div', { text: noData ? 'Press “Find jobs” and Specter will search for roles that fit you.' : state.status !== 'active' ? 'Jobs you mark will show up here.' : 'Try loosening a filter.' }),
        noData && canRun ? el('button', { class: 'primary', style: 'border-radius:999px', onclick: () => startRun(), text: 'Find jobs' }) : null,
        !noData && hasFilters() ? el('button', { class: 'btn', onclick: resetFilters, text: 'Clear filters' }) : null);
    }
  }

  function avatarFor(job, extra) {
    return el('div', { class: 'avatar' + (extra ? ' ' + extra : ''), style: '--h:' + hue(job.company || '?'), text: (job.company || '?').trim().charAt(0).toUpperCase() });
  }

  function statusButton(job, status, labels, cls, after) {
    const on = job.status === status;
    return el('button', {
      class: cls + (on ? ' on' : ''), title: labels.title(on), 'aria-label': labels.title(on), type: 'button',
      onclick: (e) => { e.stopPropagation(); setStatus(job, status).then(after).catch((err) => toast(err.message)); },
      text: labels.text(on),
    });
  }

  function row(job, idx) {
    const isNew = job.first_seen && (Date.now() - new Date(job.first_seen)) < 3 * 864e5;
    const hits = new Set((job.matches || []).map(lc));
    const tags = [];
    if (job.global_remote) tags.push(el('span', { class: 'tag good', text: 'Worldwide' }));
    if (!job.global_remote && job.policy) tags.push(el('span', { class: 'tag', title: 'Who can apply', text: job.policy }));
    if (job.contractor_friendly) tags.push(el('span', { class: 'tag good', text: 'Contractor' }));
    if (job.closed) tags.push(el('span', { class: 'tag closed', text: 'Closed' }));
    if (['applied', 'interview', 'offer'].includes(job.status)) tags.push(el('span', { class: 'tag stage', text: stageText(job) }));
    if (isDue(job)) tags.push(el('span', { class: 'tag due', text: 'Follow up' }));
    if (isNew) tags.push(el('span', { class: 'tag warn', text: 'New' }));
    if (job.seniority && job.seniority !== 'Mid/Standard') tags.push(el('span', { class: 'tag', text: shortLevel(job.seniority) }));
    if (job.compensation) tags.push(el('span', { class: 'tag', text: job.compensation }));
    (job.keywords || []).slice(0, 6).forEach((k) => tags.push(el('span', { class: 'tag' + (hits.has(lc(k)) ? ' hit' : ''), text: k })));

    const when = ago(job.first_seen);
    const src = sourceOf(job);
    const meta = [job.company, job.location, when, src && 'via ' + src].filter(Boolean).join(' · ');
    const link = safeUrl(job.apply_url);
    const stop = (e) => e.stopPropagation();

    const article = el('article', { class: 'job ' + (job.status || ''), tabindex: '-1' },
      avatarFor(job),
      el('div', {},
        el('a', { class: 'title', href: link, target: '_blank', rel: 'noopener noreferrer', onclick: stop, text: job.title }),
        el('div', { class: 'meta', text: meta }),
        tags.length ? el('div', { class: 'tags' }, ...tags) : null),
      el('div', { class: 'side' },
        job.score > 0 ? el('span', { class: 'score' + (job.score >= 80 ? ' hi' : ''), title: (job.matches || []).join(', '), text: job.score + '% match' }) : null,
        el('div', { class: 'acts' },
          statusButton(job, 'saved', { title: () => 'Save', text: (on) => (on ? '★' : '☆') }, 'icon'),
          statusButton(job, 'applied', { title: () => 'Mark as applied', text: () => '✓' }, 'icon applied'),
          statusButton(job, 'hidden', { title: (on) => (on ? 'Unhide' : 'Hide'), text: (on) => (on ? '↺' : '✕') }, 'icon'),
          el('a', { class: 'apply', href: link, target: '_blank', rel: 'noopener noreferrer', onclick: stop, text: 'Apply ↗' }))));
    article.addEventListener('click', () => { state.sel = state.jobs.indexOf(job); markSel(); openDrawer(job); });
    return article;
  }
  const startOfToday = () => { const d = new Date(); d.setHours(0, 0, 0, 0); return d; };
  function isDue(job) { if (!job.follow_up_at || job.status === 'hidden') return false; const d = new Date(job.follow_up_at); if (isNaN(d) || d.getFullYear() < 2000) return false; const end = startOfToday(); end.setDate(end.getDate() + 1); return d < end; }
  function stageText(job) { const label = (STAGES.find((x) => x[0] === job.status) || [0, job.status])[1]; const a = job.applied_at && ago(job.applied_at); return label + (a ? ' · ' + a : ''); }
  function followText(job) {
    if (!job.follow_up_at) return '';
    const d = new Date(job.follow_up_at); if (isNaN(d) || d.getFullYear() < 2000) return '';
    const days = Math.round((d - startOfToday()) / 864e5);
    if (days < -30) return 'Follow-up overdue';
    if (days < 0) return 'Follow-up ' + Math.abs(days) + 'd overdue';
    if (days === 0) return 'Follow up today';
    return 'Follow up in ' + days + 'd';
  }
  const dateInput = (iso) => { const d = new Date(iso); return isNaN(d) || d.getFullYear() < 2000 ? '' : d.toISOString().slice(0, 10); };
  const SOURCES = { remoteok: 'RemoteOK', hn: 'Hacker News', remotive: 'Remotive', jobicy: 'Jobicy', himalayas: 'Himalayas', arbeitnow: 'Arbeitnow' };
  const sourceOf = (job) => SOURCES[String(job.id).split('-')[0]] || '';
  const shortLevel = (s) => s.replace('Staff/Principal', 'Staff+').replace('Lead/Engineering Manager', 'Lead');

  async function setStatus(job, status) {
    const next = job.status === status ? '' : status;
    await api('/api/status', { method: 'POST', body: JSON.stringify({ id: job.id, status: next }) });
    job.status = next;
    loadFacets().catch(() => {});
    const visible = { active: next !== 'hidden', all: true }[state.status] ?? (next === state.status);
    if (!visible) { state.jobs = state.jobs.filter((j) => j !== job); state.total--; if (state.drawerJob === job) closeDrawer(); }
    render(); markSel();
    if (state.drawerJob === job) openDrawer(job);
    if (state.view === 'board') loadBoard().catch(() => {});
  }

  // ---------- keyboard selection ----------
  function markSel() {
    const rows = $('list').children;
    [...rows].forEach((r, i) => r.classList.toggle('sel', i === state.sel));
    if (rows[state.sel]) rows[state.sel].scrollIntoView({ block: 'nearest' });
  }
  function moveSel(d) {
    if (!state.jobs.length) return;
    state.sel = Math.max(0, Math.min(state.jobs.length - 1, state.sel + d));
    markSel();
    if (!$('drawer').hidden) openDrawer(state.jobs[state.sel]);
  }

  // ---------- drawer ----------
  function closeDrawer() { $('drawer').hidden = true; $('scrim').hidden = true; state.drawerJob = null; }

  function openDrawer(job) {
    state.drawerJob = job;
    const d = $('drawer');
    const matched = new Set((job.matches || []).map(lc));
    const extra = (job.keywords || []).filter((k) => !matched.has(lc(k)));
    const link = safeUrl(job.apply_url);
    const site = job.domain && !/\.unknown$/.test(job.domain) ? 'https://' + job.domain : '';
    const fact = (k, v) => (v ? [el('dt', { text: k }), el('dd', {}, typeof v === 'string' ? document.createTextNode(v) : v)] : []);
    const first = job.first_seen && ago(job.first_seen) ? new Date(job.first_seen).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' }) + ' (' + ago(job.first_seen) + ')' : '';

    d.replaceChildren(
      el('div', { class: 'd-head' },
        avatarFor(job),
        el('div', {}, el('h2', { text: job.title }), el('div', { class: 'meta', text: job.company })),
        el('button', { class: 'x', 'aria-label': 'Close', onclick: closeDrawer, text: '✕' })),
      el('div', { class: 'd-scroll' },
        el('dl', { class: 'facts' },
          ...fact('Location', job.location), ...fact('Remote', job.policy), ...fact('Level', job.seniority && shortLevel(job.seniority)),
          ...fact('Pay', job.compensation), ...fact('First seen', first),
          ...fact('Company site', site && el('a', { href: site, target: '_blank', rel: 'noopener noreferrer', class: 'link', text: job.domain }))),
        job.score > 0 ? el('div', { class: 'sec' },
          el('h3', { text: 'Skill match · ' + job.score + '%' }),
          el('div', { class: 'meter' + (job.score >= 80 ? ' hi' : '') }, el('i', { style: 'width:' + job.score + '%' })),
          matched.size ? el('div', { class: 'tags' }, ...(job.matches || []).map((m) => el('span', { class: 'tag hit', text: m }))) : null) : null,
        trackingSection(job),
        extra.length ? el('div', { class: 'sec' }, el('h3', { text: 'Also mentioned' }), el('div', { class: 'tags' }, ...extra.map((k) => el('span', { class: 'tag', text: k })))) : null),
      el('div', { class: 'd-foot' },
        el('a', { class: 'btn go grow', href: link, target: '_blank', rel: 'noopener noreferrer', style: 'display:grid;place-items:center', text: 'Apply ↗' }),
        statusButton(job, 'hidden', { title: (on) => (on ? 'Unhide' : 'Hide'), text: (on) => (on ? 'Unhide' : 'Hide') }, 'btn quiet')));
    d.hidden = false; $('scrim').hidden = false;
  }

  // ---------- tracking (drawer) ----------
  async function track(job, patch) {
    await api('/api/status', { method: 'POST', body: JSON.stringify({ id: job.id, ...patch }) });
    loadFacets().catch(() => {});
    if (state.view === 'board') loadBoard().catch(() => {});
  }

  function trackingSection(job) {
    const hint = el('div', { class: 'savedhint' });
    const flash = (t) => { hint.textContent = t; setTimeout(() => { hint.textContent = ''; }, 1600); };
    const refresh = () => { render(); markSel(); openDrawer(job); };

    const pills = el('div', { class: 'stagepills' }, ...STAGES.map(([id, label]) => el('button', {
      class: 'pill' + (job.status === id ? ' on' : ''), type: 'button', text: label,
      onclick: async () => {
        const next = job.status === id ? '' : id;
        try {
          await track(job, { status: next });
          const d = await api('/api/pipeline').catch(() => null);
          const fresh = d && d.stages.flatMap((st) => st.jobs).find((j) => j.id === job.id);
          Object.assign(job, { status: next, applied_at: fresh ? fresh.applied_at : job.applied_at, follow_up_at: fresh ? fresh.follow_up_at : '' });
          if (!next) { job.follow_up_at = ''; }
          refresh();
        } catch (e) { toast(e.message); }
      },
    })));

    const note = el('textarea', { class: 'note', placeholder: 'Notes: recruiter, salary talk, interview prep…', maxlength: '4000', 'aria-label': 'Notes' });
    note.value = job.note || '';
    note.addEventListener('blur', async () => {
      if (note.value === (job.note || '')) return;
      try { await track(job, { note: note.value }); job.note = note.value; flash('Saved'); } catch (e) { toast(e.message); }
    });

    const date = el('input', { type: 'date', value: dateInput(job.follow_up_at), 'aria-label': 'Follow-up date' });
    date.addEventListener('change', async () => {
      try { await track(job, { follow_up: date.value }); job.follow_up_at = date.value ? date.value + 'T00:00:00Z' : ''; flash(date.value ? 'Reminder set' : 'Reminder cleared'); render(); markSel(); } catch (e) { toast(e.message); }
    });

    return el('div', { class: 'sec' },
      el('h3', { text: 'Tracking' }), pills,
      job.status ? el('div', { class: 'followrow' }, el('span', { class: 'muted', text: 'Follow up on' }), date,
        job.follow_up_at ? el('button', { class: 'link', type: 'button', text: 'Clear', onclick: () => { date.value = ''; date.dispatchEvent(new Event('change')); } }) : null,
        isDue(job) ? el('span', { class: 'tag due', text: followText(job) }) : null) : null,
      el('div', { style: 'margin-top:12px' }, note), hint);
  }

  // ---------- board ----------
  async function loadBoard() {
    state.pipeline = await api('/api/pipeline');
    renderBoard();
    const due = state.pipeline.follow_ups_due;
    $('due-badge').hidden = !due; $('due-badge').textContent = due ? due + ' due' : '';
  }

  function renderBoard() {
    const data = state.pipeline; if (!data) return;
    fill($('board'), ...data.stages.map((st) => {
      const col = el('div', { class: 'col', 'data-stage': st.id },
        el('h3', {}, el('span', { text: st.label }), el('b', { text: String(st.jobs.length) })));
      col.addEventListener('dragover', (e) => { e.preventDefault(); col.classList.add('over'); });
      col.addEventListener('dragleave', () => col.classList.remove('over'));
      col.addEventListener('drop', async (e) => {
        e.preventDefault(); col.classList.remove('over');
        const id = e.dataTransfer.getData('text/plain');
        const job = data.stages.flatMap((x) => x.jobs).find((j) => j.id === id);
        if (job && job.status !== st.id) moveTo(job, st.id);
      });
      if (!st.jobs.length) col.append(el('div', { class: 'empty-col', text: st.id === 'saved' ? 'Save jobs to track them' : 'Drag jobs here' }));
      st.jobs.forEach((j) => col.append(boardCard(j, st.id)));
      return col;
    }));
  }

  function boardCard(job, stageId) {
    const idx = STAGES.findIndex((x) => x[0] === stageId);
    const next = STAGES[idx + 1] && stageId !== 'offer' ? STAGES[idx + 1] : null;
    const due = isDue(job); const f = followText(job);
    const card = el('article', { class: 'bcard', draggable: 'true', tabindex: '0' },
      el('div', { class: 't', text: job.title }),
      el('div', { class: 'c', text: job.company }),
      job.note ? el('div', { class: 'n', text: job.note }) : null,
      el('div', { class: 'meta2' },
        job.closed ? el('span', { class: 'tag closed', text: 'Closed' }) : null,
        job.applied_at && ago(job.applied_at) ? el('span', { class: 'tag', text: 'Applied ' + ago(job.applied_at) }) : null,
        f ? el('span', { class: 'tag' + (due ? ' due' : ''), text: f }) : null,
        next ? el('button', { class: 'go', type: 'button', title: 'Move to ' + next[1], 'aria-label': 'Move to ' + next[1], text: '→', onclick: (e) => { e.stopPropagation(); moveTo(job, next[0]); } }) : null));
    card.addEventListener('dragstart', (e) => { e.dataTransfer.setData('text/plain', job.id); e.dataTransfer.effectAllowed = 'move'; card.classList.add('dragging'); });
    card.addEventListener('dragend', () => card.classList.remove('dragging'));
    card.addEventListener('click', () => openDrawer(job));
    card.addEventListener('keydown', (e) => { if (e.key === 'Enter') openDrawer(job); });
    return card;
  }

  async function moveTo(job, stage) {
    try { await track(job, { status: stage }); await loadBoard(); if (state.drawerJob && state.drawerJob.id === job.id) { const j = state.pipeline.stages.flatMap((x) => x.jobs).find((x) => x.id === job.id); if (j) openDrawer(j); } }
    catch (e) { toast(e.message); }
  }

  function setView(view) {
    state.view = view;
    document.querySelectorAll('#views button').forEach((b) => b.classList.toggle('on', b.dataset.view === view));
    const board = view === 'board';
    document.body.classList.toggle('wide', board);
    $('board').hidden = !board;
    ['filters', 'listhead', 'list', 'hero'].forEach((id) => { $(id).hidden = board || (id === 'hero' && (!state.profile || !state.profile.configured)); });
    $('loadmore').hidden = board || state.jobs.length >= state.total;
    $('empty').hidden = board || state.total !== 0;
    try { const u = new URL(location.href); board ? u.searchParams.set('view', 'board') : u.searchParams.delete('view'); history.replaceState(null, '', u); } catch { /* ignore */ }
    if (board) loadBoard().catch(showError); else render();
  }

  // ---------- facets / filters UI ----------
  async function loadFacets() {
    const f = await api('/api/facets');
    state.facets = f;
    document.title = 'Specter' + (f.total ? ' · ' + f.total : '');
    $('due-badge').hidden = !f.follow_ups; $('due-badge').textContent = f.follow_ups ? f.follow_ups + ' due' : '';
    $('keywords').replaceChildren(...(f.keywords || []).slice(0, 24).map((k) => el('button', {
      class: 'pill' + (state.keywords.has(k.name) ? ' on' : ''), text: k.name,
      onclick: (e) => { state.keywords.has(k.name) ? state.keywords.delete(k.name) : state.keywords.add(k.name); e.currentTarget.classList.toggle('on'); search().catch(showError); },
    })));
    syncTabs(); renderHero();
  }

  function renderLevels() {
    $('levels').replaceChildren(...LEVELS.map(([val, label]) => el('button', {
      class: 'pill' + (state.levels.has(val) ? ' on' : ''), text: label,
      onclick: (e) => { state.levels.has(val) ? state.levels.delete(val) : state.levels.add(val); e.currentTarget.classList.toggle('on'); search().catch(showError); },
    })));
  }

  function resetFilters() {
    $('q').value = ''; $('company').value = ''; $('fresh').value = '0'; $('min_score').value = '0';
    pressed('global', false); pressed('contractor', false);
    state.levels.clear(); state.keywords.clear(); state.status = 'active';
    renderLevels(); loadFacets().then(() => search()).catch(showError);
  }

  function showError(err) {
    const e = $('empty'); e.hidden = false; e.replaceChildren(el('h2', { text: 'Something went wrong' }), el('div', { text: err.message }));
  }

  // ---------- profile chip ----------
  function meBadge(prefs) {
    if (prefs.avatar) return el('img', { class: 'me', src: prefs.avatar, alt: '' });
    return el('span', { class: 'me', text: (prefs.name || prefs.github || '•').trim().charAt(0).toUpperCase() });
  }
  function renderChip() {
    const pr = state.profile; const chip = $('profile-chip');
    if (!pr || !pr.configured) { chip.hidden = true; return; }
    chip.hidden = false;
    const preset = pr.presets.find((p) => p.id === pr.prefs.role);
    chip.replaceChildren(meBadge(pr.prefs), el('b', { text: (pr.prefs.name || '').split(' ')[0] || (preset ? preset.label : 'Profile') }));
    chip.title = 'Edit your profile (' + (preset ? preset.label : pr.prefs.role) + ')';
  }

  // ---------- running ----------
  function renderRun() {
    const st = state.run; const running = st.state === 'running';
    $('run').disabled = running; $('run-menu-btn').disabled = running;
    $('run').textContent = running ? 'Searching…' : 'Find jobs';
    $('runbar').hidden = !running;
    if (!running) return;
    const fill = $('runfill');
    if (st.total > 0 && st.done < st.total) { fill.classList.remove('indet'); fill.style.width = Math.max(4, (st.done / st.total) * 100) + '%'; }
    else { fill.classList.add('indet'); fill.style.width = ''; }
    $('runtext').textContent = st.total > 0 && st.done < st.total
      ? `Searching ${st.done} of ${st.total} companies${st.current ? ' · ' + st.current : ''}`
      : (st.total === 0 && !st.current ? 'Starting…' : `Checking job boards${st.current ? ' · ' + st.current : ''}…`);
  }

  async function pollRun() {
    clearTimeout(state.pollTimer);
    let st;
    try { st = await api('/api/run'); } catch { state.pollTimer = setTimeout(pollRun, 2000); return; }
    state.run = st; renderRun();
    if (st.state === 'running') {
      state.wasRunning = true;
      loadFacets().catch(() => {});
      state.pollTimer = setTimeout(pollRun, 1000);
      return;
    }
    if (state.wasRunning) {
      state.wasRunning = false;
      if (st.state === 'done') toast(st.new_roles ? '✓ ' + plural(st.new_roles, 'new job') + ' found' : 'Up to date — no new jobs this time');
      else if (st.state === 'cancelled') toast('Search cancelled');
      else if (st.state === 'error') toast('Search failed: ' + st.error, 6000);
      await loadFacets(); await search().catch(showError);
    }
  }

  async function startRun(mode) {
    if (!state.profile || !state.profile.configured) { openSetup('first'); return; }
    mode = mode || store.get('mode', 'quick');
    store.set('mode', mode);
    try {
      state.run = await api('/api/run', { method: 'POST', body: JSON.stringify({ mode }) });
      state.wasRunning = true; renderRun(); pollRun();
    } catch (e) { toast(e.message, 5000); }
  }

  // ---------- setup wizard ----------
  const SCAN_LINES = ['Reading your public profile…', 'Looking through your repositories…', 'Spotting languages and tools…', 'Working out your role…'];

  function openSetup(mode) {
    const pr = state.profile;
    const first = mode === 'first';
    const existing = pr.configured ? pr.prefs : null;
    const draft = existing ? JSON.parse(JSON.stringify(existing))
      : { name: '', github: '', avatar: '', role: 'software-engineer', skills: [], seniority: [], worldwide_only: false, feeds: true };
    draft.skills = draft.skills || []; draft.seniority = draft.seniority || [];
    const presetOf = (id) => pr.presets.find((p) => p.id === id);
    let step = first ? 'intro' : 'profile';
    let detected = null; let err = ''; let query = ''; let scanTimer = null; let skillsTouched = !!existing; let showAlso = false;
    if (!draft.skills.length) draft.skills = presetOf(draft.role).skills.slice();

    const dlg = $('setup');
    const hasSkill = (s) => draft.skills.some((x) => lc(x) === lc(s));
    const toggleSkill = (s) => { const i = draft.skills.findIndex((x) => lc(x) === lc(s)); i < 0 ? draft.skills.push(s) : draft.skills.splice(i, 1); skillsTouched = true; };

    const chip = (label, title, after) => el('button', {
      class: 'sk', type: 'button', title: title || '', 'aria-pressed': hasSkill(label) ? 'true' : 'false',
      onclick: (e) => { toggleSkill(label); e.currentTarget.setAttribute('aria-pressed', hasSkill(label) ? 'true' : 'false'); after(); },
    }, label);

    function draw() {
      clearInterval(scanTimer);
      dlg.oncancel = (e) => { if (first) e.preventDefault(); };
      if (step === 'intro') return drawIntro();
      if (step === 'scan') return drawScan();
      drawProfile();
    }

    // -- step 1: who are you
    function drawIntro() {
      const name = el('input', { class: 'input', placeholder: 'Ada Lovelace', value: draft.name, autocomplete: 'name', 'aria-label': 'Your name' });
      const gh = el('input', { class: 'input', placeholder: 'octocat', value: draft.github, autocomplete: 'off', spellcheck: 'false', 'aria-label': 'GitHub username' });
      const go = () => { draft.name = name.value.trim(); draft.github = gh.value.trim(); draft.github ? runImport() : (step = 'profile', draw()); };
      [name, gh].forEach((i) => i.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); go(); } }));
      fill(dlg,
        el('div', { class: 'w-body' },
          el('div', { class: 'steps' }, el('i', { class: 'on' }), el('i'), el('i')),
          el('h2', { id: 'setup-title', text: first ? 'Welcome to Specter' : 'Import from GitHub' }),
          el('p', { text: 'Tell us who you are and we’ll find jobs that fit you.' }),
          err ? el('div', { class: 'err', role: 'alert', text: err }) : null,
          el('label', { class: 'field' }, el('span', { text: 'Your name' }), name),
          pr.can_import ? el('label', { class: 'field' }, el('span', { text: 'GitHub username' }), el('div', { class: 'prefixed' }, el('em', { text: 'github.com/' }), gh)) : null,
          pr.can_import ? el('div', { class: 'note' }, el('span', { text: '🔒' }), el('span', { text: 'We read your public repositories (languages, topics) to suggest your skills and role. It happens on your computer; nothing is stored anywhere else.' })) : null),
        el('div', { class: 'w-foot' },
          el('span', { class: 'grow' }),
          el('button', { class: 'btn quiet', type: 'button', onclick: () => { draft.name = name.value.trim(); step = 'profile'; err = ''; draw(); }, text: pr.can_import ? 'Skip, I’ll choose myself' : 'Continue' }),
          pr.can_import ? el('button', { class: 'btn go', type: 'button', onclick: go, text: 'Continue →' }) : null));
      if (!existing) (draft.name ? gh : name).focus(); else gh.focus();
    }

    // -- step 2: scanning
    function drawScan() {
      const line = el('div', { text: SCAN_LINES[0] });
      let i = 0;
      scanTimer = setInterval(() => { i = (i + 1) % SCAN_LINES.length; line.textContent = SCAN_LINES[i]; }, 1300);
      dlg.oncancel = (e) => e.preventDefault();
      dlg.replaceChildren(el('div', { class: 'w-body' },
        el('div', { class: 'steps' }, el('i', { class: 'on' }), el('i', { class: 'on' }), el('i')),
        el('div', { class: 'scan' }, el('div', { class: 'spin' }), line, el('div', { class: 'hint', text: 'github.com/' + draft.github }))));
    }

    async function runImport() {
      err = ''; step = 'scan'; draw();
      try {
        const d = await api('/api/github/import', { method: 'POST', body: JSON.stringify({ input: draft.github }) });
        detected = d;
        draft.github = d.login; draft.name = draft.name || d.name; draft.avatar = d.avatar || draft.avatar; draft.role = d.role;
        const picked = d.skills.filter((s) => s.selected).map((s) => s.name);
        draft.skills = picked.length ? picked : presetOf(d.role).skills.slice();
        skillsTouched = true; step = 'profile';
      } catch (e) { err = e.message; step = 'intro'; }
      draw();
    }

    // -- step 3: review and adjust
    function drawProfile() {
      const rolesBox = el('div', { class: 'roles' });
      const selBox = el('div', { class: 'sk-row' });
      const selCount = el('span', { class: 'sel-count' });
      const detBox = el('div', { class: 'sk-row' });
      const browse = el('div', { class: 'sk-box' });
      const levels = el('div', { class: 'pills' });
      const skSearch = el('input', { class: 'input sk-search', placeholder: 'Search skills, or type your own and press Enter', 'aria-label': 'Search skills', value: query, autocomplete: 'off' });

      const order = detected ? detected.roles.concat(pr.presets.map((p) => p.id).filter((id) => !detected.roles.includes(id))) : pr.presets.map((p) => p.id);
      const drawRoles = () => rolesBox.replaceChildren(...order.map((id) => {
        const p = presetOf(id);
        return el('button', {
          class: 'role' + (id === draft.role ? ' on' : ''), type: 'button',
          onclick: () => { draft.role = id; if (!skillsTouched) draft.skills = p.skills.slice(); drawRoles(); drawSkills(); },
        }, detected && id === detected.role ? el('span', { class: 'best', text: 'Best match' }) : null, el('b', { text: p.emoji + '  ' + p.label }), el('span', { text: p.blurb }));
      }));

      const drawSkills = () => {
        selCount.textContent = draft.skills.length ? '· ' + draft.skills.length + ' selected' : '';
        selBox.replaceChildren(...draft.skills.map((s) => el('button', {
          class: 'sk', type: 'button', 'aria-pressed': 'true', title: 'Remove', onclick: () => { toggleSkill(s); drawSkills(); },
        }, s + '  ×')));
        if (!draft.skills.length) selBox.append(el('span', { class: 'hint', text: 'Nothing selected: we’ll use the defaults for your role.' }));

        const q = lc(query.trim());
        const tip = (sk) => [sk.repos ? plural(sk.repos, 'repo') : '', sk.last_used && ago(sk.last_used) ? 'last used ' + ago(sk.last_used) : '', sk.from_profile ? 'mentioned in your profile' : ''].filter(Boolean).join(' · ');
        const strong = detected ? detected.skills.filter((x) => x.selected) : [];
        const weak = detected ? detected.skills.filter((x) => !x.selected) : [];
        const detNames = strong.concat(weak).map((x) => x.name);
        const matches = (x) => !q || lc(x.name).includes(q);
        const alsoLink = weak.length && !q
          ? el('button', { class: 'link', type: 'button', onclick: () => { showAlso = !showAlso; drawSkills(); }, text: showAlso ? 'Show fewer' : 'Show ' + weak.length + ' more found on GitHub' })
          : null;
        fill(detBox, ...strong.filter(matches).map((x) => chip(x.name, tip(x), drawSkills)),
          ...(showAlso || q ? weak.filter(matches).map((x) => chip(x.name, tip(x), drawSkills)) : []), alsoLink);
        detBox.parentElement && (detBox.parentElement.hidden = !detNames.length);

        const groups = pr.catalog.map((g) => ({ category: g.category, skills: g.skills.filter((s) => !detNames.includes(s) && (!q || lc(s).includes(q))) })).filter((g) => g.skills.length);
        const known = pr.catalog.some((g) => g.skills.some((s) => lc(s) === q)) || detNames.some((s) => lc(s) === q);
        fill(browse, ...groups.map((g) => el('div', { class: 'sk-group' }, el('h4', { text: g.category }), el('div', { class: 'sk-row' }, ...g.skills.map((s) => chip(s, '', drawSkills))))),
          q && !known ? el('button', { class: 'sk add', type: 'button', onclick: () => { addCustom(); }, text: '+ Add “' + query.trim() + '”' }) : null,
          !groups.length && !q ? el('span', { class: 'hint', text: 'No more skills to browse.' }) : null);
      };
      const addCustom = () => { const v = query.trim(); if (v && !hasSkill(v)) { const canon = null; draft.skills.push(canon || v); skillsTouched = true; } query = ''; skSearch.value = ''; drawSkills(); };
      skSearch.addEventListener('input', () => { query = skSearch.value; drawSkills(); });
      skSearch.addEventListener('keydown', (e) => {
        if (e.key === 'Enter') { e.preventDefault(); const q = lc(query.trim()); const exact = [].concat(...pr.catalog.map((g) => g.skills)).find((s) => lc(s) === q); if (exact) { if (!hasSkill(exact)) toggleSkill(exact); query = ''; skSearch.value = ''; drawSkills(); } else addCustom(); }
      });

      const drawLevels = () => levels.replaceChildren(...LEVELS.map(([val, label]) => el('button', {
        type: 'button', class: 'pill' + (draft.seniority.includes(val) ? ' on' : ''), text: label,
        onclick: () => { const i = draft.seniority.indexOf(val); i < 0 ? draft.seniority.push(val) : draft.seniority.splice(i, 1); drawLevels(); },
      })));
      const check = (label, key) => { const cb = el('input', { type: 'checkbox' }); cb.checked = !!draft[key]; cb.addEventListener('change', () => { draft[key] = cb.checked; }); return el('label', { class: 'toggle' }, cb, label); };

      const save = async (thenRun) => {
        try { await api('/api/profile', { method: 'PUT', body: JSON.stringify(draft) }); } catch (e) { toast(e.message, 5000); return; }
        dlg.close();
        state.profile = await api('/api/profile'); renderChip(); $('views').hidden = false;
        if (first || !hasFilters()) { pressed('global', !!draft.worldwide_only); state.levels = new Set(draft.seniority); renderLevels(); }
        await loadFacets(); await search().catch(showError);
        if (thenRun) startRun();
      };

      const identity = (draft.github || draft.name)
        ? el('div', { class: 'who' }, meBadge(draft),
          el('div', {}, el('b', { text: draft.name || draft.github }),
            draft.github ? el('span', { text: '@' + draft.github + (detected ? ' · read ' + plural(detected.repos_analyzed, 'repository').replace('repositorys', 'repositories') + (detected.readme_used ? ' and your profile README' : '') : '') }) : null,
            detected && detected.bio ? el('p', { text: detected.bio }) : null))
        : null;

      const detWrap = el('div', {}, el('span', { class: 'w-label' }, 'From your GitHub', el('span', { class: 'hint', text: 'what you use most, recent work first' })), detBox);

      fill(dlg,
        el('div', { class: 'w-body' },
          first ? el('div', { class: 'steps' }, el('i', { class: 'on' }), el('i', { class: 'on' }), el('i', { class: 'on' })) : null,
          el('h2', { id: 'setup-title', text: detected ? 'Here’s what we found' : first ? 'What kind of work are you after?' : 'Your profile' }),
          el('p', { text: detected ? 'Adjust anything. Tap to select or deselect.' : first ? 'Pick your role and the skills you want to use.' : 'Changes apply to your next search.' }),
          identity,
          el('span', { class: 'w-label', text: 'Role' }), rolesBox,
          el('span', { class: 'w-label' }, 'Your skills ', selCount), selBox,
          detWrap,
          el('span', { class: 'w-label', text: 'Add more' }), skSearch, browse,
          el('span', { class: 'w-label' }, 'Level ', el('span', { class: 'hint', text: 'optional' })), levels,
          el('span', { class: 'w-label', text: 'Preferences' }),
          check('Worldwide remote only', 'worldwide_only'),
          check('Also search RemoteOK & Hacker News “Who is hiring”', 'feeds')),
        el('div', { class: 'w-foot' },
          pr.can_import ? el('button', { class: 'btn quiet', type: 'button', onclick: () => { step = 'intro'; err = ''; draw(); }, text: detected || draft.github ? '← GitHub' : 'Import from GitHub' }) : null,
          el('span', { class: 'grow' }),
          first ? null : el('button', { class: 'btn', type: 'button', onclick: () => dlg.close(), text: 'Cancel' }),
          first ? null : el('button', { class: 'btn', type: 'button', onclick: () => save(false), text: 'Save' }),
          el('button', { class: 'btn go', type: 'button', onclick: () => save(pr.can_run), text: pr.can_run ? (first ? 'Find my jobs' : 'Save & search') : 'Save' })));
      drawRoles(); drawSkills(); drawLevels();
    }

    draw();
    if (!dlg.open) dlg.showModal();
  }

  // ---------- init ----------
  async function init() {
    const wantBoard = new URLSearchParams(location.search).get('view') === 'board';
    renderTheme();
    const hadQuery = restoreFromURL();
    renderLevels();

    const go = () => search().catch(showError);
    const goSoon = debounce(go, 180);
    $('q').addEventListener('input', goSoon);
    $('company').addEventListener('input', goSoon);
    $('fresh').addEventListener('change', go); $('min_score').addEventListener('change', go); $('sort').addEventListener('change', go);
    ['global', 'contractor'].forEach((id) => $(id).addEventListener('click', () => { pressed(id, !isOn(id)); go(); }));
    $('more-toggle').addEventListener('click', () => { const open = $('more').hidden; $('more').hidden = !open; $('more-toggle').setAttribute('aria-expanded', open); $('more-toggle').classList.toggle('on', open); });
    $('reset').addEventListener('click', resetFilters);
    document.querySelectorAll('#tabs button').forEach((b) => b.addEventListener('click', () => { state.status = b.dataset.status; syncTabs(); go(); }));
    $('loadmore').addEventListener('click', () => { state.offset = state.jobs.length; search(true).catch(showError); });
    $('profile-chip').addEventListener('click', () => openSetup('edit'));
    $('theme').addEventListener('click', cycleTheme);
    document.querySelectorAll('#views button').forEach((b) => b.addEventListener('click', () => setView(b.dataset.view)));
    $('scrim').addEventListener('click', closeDrawer);

    $('run').addEventListener('click', () => startRun());
    $('run-menu-btn').addEventListener('click', (e) => { e.stopPropagation(); $('run-menu').hidden = !$('run-menu').hidden; });
    document.querySelectorAll('#run-menu button').forEach((b) => b.addEventListener('click', () => { $('run-menu').hidden = true; startRun(b.dataset.mode); }));
    document.addEventListener('click', () => { $('run-menu').hidden = true; });
    $('cancel').addEventListener('click', () => api('/api/run/cancel', { method: 'POST', body: '{}' }).catch((e) => toast(e.message)));

    document.addEventListener('keydown', (e) => {
      const tag = document.activeElement && document.activeElement.tagName;
      const typing = ['INPUT', 'TEXTAREA', 'SELECT'].includes(tag);
      if (e.key === 'Escape') { $('run-menu').hidden = true; if (!$('drawer').hidden) closeDrawer(); return; }
      if (typing || e.metaKey || e.ctrlKey || e.altKey || $('setup').open) return;
      if (e.key === '/') { e.preventDefault(); $('q').focus(); }
      else if (e.key === 'j') moveSel(1);
      else if (e.key === 'k') moveSel(-1);
      else if (e.key === 'Enter' && state.jobs[state.sel] && tag !== 'A' && tag !== 'BUTTON') openDrawer(state.jobs[state.sel]);
    });

    try {
      state.profile = await api('/api/profile');
      if (!state.profile.can_run) $('run').parentElement.hidden = true;
      renderChip();
      if (state.profile.configured && !hadQuery) {
        const pf = state.profile.prefs;
        pressed('global', !!pf.worldwide_only); state.levels = new Set(pf.seniority || []); renderLevels();
      }
      $('views').hidden = !state.profile.configured;
      await loadFacets(); await search();
      if (wantBoard && state.profile.configured) setView('board');
      if (!state.profile.configured) openSetup('first');
      pollRun();
    } catch (e) { showError(e); }
  }

  init();
})();
