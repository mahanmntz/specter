(() => {
  'use strict';
  const PAGE = 40;
  const LEVELS = [['Junior', 'Junior'], ['Mid/Standard', 'Mid'], ['Senior', 'Senior'], ['Staff/Principal', 'Staff+'], ['Lead/Engineering Manager', 'Lead']];
  const $ = (id) => document.getElementById(id);
  const state = {
    status: 'active', levels: new Set(), keywords: new Set(), offset: 0, jobs: [], total: 0,
    facets: null, profile: null, run: { state: 'idle' }, wasRunning: false, pollTimer: null,
  };

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
  const store = { get: (k, d) => { try { return localStorage.getItem(k) || d; } catch { return d; } }, set: (k, v) => { try { localStorage.setItem(k, v); } catch { /* ignore */ } } };

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

  // ---------- filters / search ----------
  const active = () => $('global').getAttribute('aria-pressed') === 'true';
  const pressed = (id, on) => $(id).setAttribute('aria-pressed', on ? 'true' : 'false');

  function params(offset) {
    const p = new URLSearchParams();
    const q = $('q').value.trim(); if (q) p.set('q', q);
    if (state.status !== 'active') p.set('status', state.status);
    if (active()) p.set('global', '1');
    if ($('contractor').getAttribute('aria-pressed') === 'true') p.set('contractor', '1');
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
    const s = params(0).toString();
    try { history.replaceState(null, '', s ? '?' + s : location.pathname); } catch { /* ignore */ }
    $('reset').hidden = !hasFilters();
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

  function syncTabs() { document.querySelectorAll('#tabs button').forEach((b) => b.classList.toggle('on', b.dataset.status === state.status)); }

  async function search(append = false) {
    if (!append) { state.offset = 0; persistURL(); }
    const p = params(state.offset); p.set('limit', PAGE);
    const data = await api('/api/jobs?' + p);
    state.total = data.total;
    state.jobs = append ? state.jobs.concat(data.jobs) : data.jobs;
    render();
  }

  // ---------- rendering ----------
  function render() {
    $('list').replaceChildren(...state.jobs.map(row));
    $('count').textContent = state.total ? plural(state.total, 'job') : '';
    $('loadmore').hidden = state.jobs.length >= state.total;

    const empty = $('empty'); empty.hidden = state.total !== 0;
    if (state.total === 0) {
      const noData = state.facets && state.facets.total === 0;
      const canRun = state.profile && state.profile.can_run && state.profile.configured;
      empty.replaceChildren(
        el('h2', { text: noData ? 'No jobs yet' : state.status !== 'active' ? 'Nothing here yet' : 'No matches' }),
        el('div', { text: noData ? 'Press “Find jobs” and Specter will search for roles that fit you.' : state.status !== 'active' ? 'Jobs you mark will show up here.' : 'Try loosening a filter.' }),
        noData && canRun ? el('button', { class: 'primary', style: 'border-radius:999px', onclick: () => startRun(), text: 'Find jobs' }) : null,
        !noData && hasFilters() ? el('button', { class: 'btn', onclick: resetFilters, text: 'Clear filters' }) : null);
    }
  }

  function row(job) {
    const isNew = job.first_seen && (Date.now() - new Date(job.first_seen)) < 3 * 864e5;
    const hits = new Set((job.matches || []).map((m) => m.toLowerCase()));
    const tags = [];
    if (job.global_remote) tags.push(el('span', { class: 'tag good', text: 'Worldwide' }));
    if (job.contractor_friendly) tags.push(el('span', { class: 'tag good', text: 'Contractor' }));
    if (isNew) tags.push(el('span', { class: 'tag warn', text: 'New' }));
    if (job.seniority && job.seniority !== 'Mid/Standard') tags.push(el('span', { class: 'tag', text: job.seniority.replace('Staff/Principal', 'Staff+').replace('Lead/Engineering Manager', 'Lead') }));
    if (job.compensation) tags.push(el('span', { class: 'tag', text: job.compensation }));
    (job.keywords || []).slice(0, 6).forEach((k) => tags.push(el('span', { class: 'tag' + (hits.has(k.toLowerCase()) ? ' hit' : ''), text: k })));

    const when = ago(job.first_seen);
    const meta = [job.company, job.location, when].filter(Boolean).join(' · ');
    const toggle = (status, on, off, label, cls = '') => el('button', {
      class: 'icon ' + cls + (job.status === status ? ' on' : ''), title: label, 'aria-label': label,
      onclick: () => setStatus(job, status).catch((e) => toast(e.message)), text: job.status === status ? on : off,
    });
    const link = safeUrl(job.apply_url);

    return el('article', { class: 'job ' + (job.status || '') },
      el('div', { class: 'avatar', style: '--h:' + hue(job.company || '?'), text: (job.company || '?').trim().charAt(0).toUpperCase() }),
      el('div', {},
        el('a', { class: 'title', href: link, target: '_blank', rel: 'noopener noreferrer', text: job.title }),
        el('div', { class: 'meta', text: meta }),
        tags.length ? el('div', { class: 'tags' }, ...tags) : null),
      el('div', { class: 'side' },
        job.score > 0 ? el('span', { class: 'score' + (job.score >= 80 ? ' hi' : ''), title: (job.matches || []).join(', '), text: job.score + '% match' }) : null,
        el('div', { class: 'acts' },
          toggle('saved', '★', '☆', 'Save'),
          toggle('applied', '✓', '✓', 'Mark as applied', 'applied'),
          toggle('hidden', '↺', '✕', job.status === 'hidden' ? 'Unhide' : 'Hide'),
          el('a', { class: 'apply', href: link, target: '_blank', rel: 'noopener noreferrer', text: 'Apply ↗' }))));
  }

  async function setStatus(job, status) {
    const next = job.status === status ? '' : status;
    await api('/api/status', { method: 'POST', body: JSON.stringify({ id: job.id, status: next }) });
    job.status = next;
    const visible = { active: next !== 'hidden', all: true }[state.status] ?? (next === state.status);
    if (!visible) { state.jobs = state.jobs.filter((j) => j !== job); state.total--; }
    render();
  }

  async function loadFacets() {
    const f = await api('/api/facets');
    state.facets = f;
    const bits = [];
    if (f.total) bits.push(plural(f.total, 'job'));
    if (f.last_run && ago(f.last_run)) bits.push('updated ' + ago(f.last_run));
    document.title = 'Specter' + (f.total ? ' · ' + f.total : '');
    $('keywords').replaceChildren(...(f.keywords || []).slice(0, 24).map((k) => el('button', {
      class: 'pill' + (state.keywords.has(k.name) ? ' on' : ''), text: k.name,
      onclick: (e) => { state.keywords.has(k.name) ? state.keywords.delete(k.name) : state.keywords.add(k.name); e.currentTarget.classList.toggle('on'); search().catch(showError); },
    })));
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
    state.levels.clear(); state.keywords.clear();
    renderLevels(); loadFacets().then(() => search()).catch(showError);
  }

  function showError(err) {
    const e = $('empty'); e.hidden = false; e.replaceChildren(el('h2', { text: 'Something went wrong' }), el('div', { text: err.message }));
  }

  // ---------- profile chip ----------
  function renderChip() {
    const pr = state.profile; const chip = $('profile-chip');
    if (!pr || !pr.configured) { chip.hidden = true; return; }
    chip.hidden = false;
    const preset = pr.presets.find((p) => p.id === pr.prefs.role);
    chip.replaceChildren(document.createTextNode((preset ? preset.emoji + ' ' : '')), el('b', { text: preset ? preset.label : pr.prefs.role }));
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
      : `Checking job boards${st.current ? ' · ' + st.current : ''}…`;
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
    if (!state.profile || !state.profile.configured) { openSetup(true); return; }
    mode = mode || store.get('mode', 'quick');
    store.set('mode', mode);
    try {
      state.run = await api('/api/run', { method: 'POST', body: JSON.stringify({ mode }) });
      state.wasRunning = true; renderRun(); pollRun();
    } catch (e) { toast(e.message, 5000); }
  }

  // ---------- setup dialog ----------
  function openSetup(first) {
    const pr = state.profile;
    const existing = pr.configured ? pr.prefs : null;
    const draft = existing ? JSON.parse(JSON.stringify(existing)) : { role: 'backend', skills: [], seniority: [], worldwide_only: false, feeds: true };
    const presetOf = (id) => pr.presets.find((p) => p.id === id);
    let dirty = !!existing && JSON.stringify(existing.skills) !== JSON.stringify((presetOf(existing.role) || {}).skills);
    if (!draft.skills || !draft.skills.length) draft.skills = presetOf(draft.role).skills.slice();

    const dlg = $('setup');
    const rolesBox = el('div', { class: 'roles' });
    const skillsBox = el('div', { class: 'skills' });
    const levelsBox = el('div', { class: 'pills' });

    const renderRoles = () => rolesBox.replaceChildren(...pr.presets.map((p) => el('button', {
      class: 'role' + (p.id === draft.role ? ' on' : ''), type: 'button',
      onclick: () => { draft.role = p.id; if (!dirty) draft.skills = p.skills.slice(); renderRoles(); renderSkills(); },
    }, el('b', { text: p.emoji + '  ' + p.label }), el('span', { text: p.blurb }))));

    const input = el('input', { placeholder: 'Add a skill…', 'aria-label': 'Add a skill' });
    const addSkill = () => {
      const v = input.value.replace(/,/g, '').trim();
      if (v && !draft.skills.some((s) => s.toLowerCase() === v.toLowerCase())) { draft.skills.push(v); dirty = true; }
      input.value = ''; renderSkills();
    };
    input.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' || e.key === ',') { e.preventDefault(); addSkill(); }
      else if (e.key === 'Backspace' && !input.value && draft.skills.length) { draft.skills.pop(); dirty = true; renderSkills(); }
    });
    input.addEventListener('blur', addSkill);
    const renderSkills = () => { skillsBox.replaceChildren(...draft.skills.map((s, i) => el('span', { class: 'skill' }, s,
      el('button', { type: 'button', 'aria-label': 'Remove ' + s, text: '×', onclick: () => { draft.skills.splice(i, 1); dirty = true; renderSkills(); } }))), input); };

    const renderLv = () => levelsBox.replaceChildren(...LEVELS.map(([val, label]) => el('button', {
      type: 'button', class: 'pill' + (draft.seniority.includes(val) ? ' on' : ''), text: label,
      onclick: () => { const i = draft.seniority.indexOf(val); i < 0 ? draft.seniority.push(val) : draft.seniority.splice(i, 1); renderLv(); },
    })));

    const check = (label, key) => { const cb = el('input', { type: 'checkbox' }); cb.checked = !!draft[key]; cb.addEventListener('change', () => { draft[key] = cb.checked; }); return el('label', { class: 'toggle' }, cb, label); };

    const save = async (thenRun) => {
      addSkill();
      try {
        await api('/api/profile', { method: 'PUT', body: JSON.stringify(draft) });
      } catch (e) { toast(e.message, 5000); return; }
      dlg.close();
      state.profile = await api('/api/profile'); renderChip();
      if (first || !hasFilters()) {
        pressed('global', !!draft.worldwide_only); state.levels = new Set(draft.seniority); renderLevels();
      }
      await loadFacets(); await search().catch(showError);
      if (thenRun) startRun();
    };

    const foot = el('div', { class: 'd-foot' },
      first ? el('span', { class: 'grow', text: 'You can change this anytime.' }) : el('span', { class: 'grow' }),
      first ? null : el('button', { class: 'btn', type: 'button', onclick: () => dlg.close(), text: 'Cancel' }),
      first ? null : el('button', { class: 'btn', type: 'button', onclick: () => save(false), text: 'Save' }),
      state.profile.can_run ? el('button', { class: 'btn go', type: 'button', onclick: () => save(true), text: first ? 'Find my jobs' : 'Save & search' })
        : el('button', { class: 'btn go', type: 'button', onclick: () => save(false), text: 'Save' }));

    dlg.replaceChildren(
      el('div', { class: 'd-body' },
        el('h2', { id: 'setup-title', text: first ? 'What kind of work are you after?' : 'Your profile' }),
        el('p', { text: first ? 'Pick your role and Specter will find matching jobs for you.' : 'Changes apply to your next search.' }),
        rolesBox,
        el('span', { class: 'd-label', text: 'Your skills' }), skillsBox,
        el('span', { class: 'd-label', text: 'Level (optional)' }), levelsBox,
        el('span', { class: 'd-label', text: 'Preferences' }),
        check('Worldwide remote only', 'worldwide_only'),
        check('Also search RemoteOK & Hacker News “Who is hiring”', 'feeds')),
      foot);
    renderRoles(); renderSkills(); renderLv();
    dlg.oncancel = (e) => { if (first) e.preventDefault(); };
    if (!dlg.open) dlg.showModal();
  }

  // ---------- init ----------
  async function init() {
    const hadQuery = restoreFromURL();
    renderLevels();

    const go = () => search().catch(showError);
    const goSoon = debounce(go, 180);
    $('q').addEventListener('input', goSoon);
    $('company').addEventListener('input', goSoon);
    $('fresh').addEventListener('change', go); $('min_score').addEventListener('change', go); $('sort').addEventListener('change', go);
    ['global', 'contractor'].forEach((id) => $(id).addEventListener('click', () => { pressed(id, $(id).getAttribute('aria-pressed') !== 'true'); go(); }));
    $('more-toggle').addEventListener('click', () => { const open = $('more').hidden; $('more').hidden = !open; $('more-toggle').setAttribute('aria-expanded', open); $('more-toggle').classList.toggle('on', open); });
    $('reset').addEventListener('click', resetFilters);
    document.querySelectorAll('#tabs button').forEach((b) => b.addEventListener('click', () => { state.status = b.dataset.status; syncTabs(); go(); }));
    $('loadmore').addEventListener('click', () => { state.offset = state.jobs.length; search(true).catch(showError); });
    $('profile-chip').addEventListener('click', () => openSetup(false));

    $('run').addEventListener('click', () => startRun());
    $('run-menu-btn').addEventListener('click', (e) => { e.stopPropagation(); $('run-menu').hidden = !$('run-menu').hidden; });
    document.querySelectorAll('#run-menu button').forEach((b) => b.addEventListener('click', () => { $('run-menu').hidden = true; startRun(b.dataset.mode); }));
    document.addEventListener('click', () => { $('run-menu').hidden = true; });
    $('cancel').addEventListener('click', () => api('/api/run/cancel', { method: 'POST', body: '{}' }).catch((e) => toast(e.message)));
    document.addEventListener('keydown', (e) => {
      if (e.key === '/' && !['INPUT', 'TEXTAREA', 'SELECT'].includes(document.activeElement.tagName)) { e.preventDefault(); $('q').focus(); }
      if (e.key === 'Escape') $('run-menu').hidden = true;
    });

    try {
      state.profile = await api('/api/profile');
      if (!state.profile.can_run) $('run').parentElement.hidden = true;
      renderChip();
      if (state.profile.configured && !hadQuery) {
        const pf = state.profile.prefs;
        pressed('global', !!pf.worldwide_only); state.levels = new Set(pf.seniority || []); renderLevels();
      }
      await loadFacets(); await search();
      if (!state.profile.configured) openSetup(true);
      pollRun();
    } catch (e) { showError(e); }
  }

  init();
})();
