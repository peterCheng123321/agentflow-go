const API = location.origin + '/v1';
const esc = s => String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');
const escA = s => String(s).replace(/&/g,'&amp;').replace(/"/g,'&quot;').replace(/</g,'&lt;');

const ICONS = {
    doc: '<svg viewBox="0 0 24 24" style="width:14px;height:14px;stroke:currentColor;fill:none;stroke-width:2"><path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8l-6-6z"/><polyline points="14 2 14 8 20 8"/></svg>',
    upload: '<svg viewBox="0 0 24 24" style="width:14px;height:14px;stroke:currentColor;fill:none;stroke-width:2"><path d="M21 15v4a2 2 0 01-2 2H5a2 2 0 01-2-2v-4"/><polyline points="17 8 12 3 7 8"/><line x1="12" y1="3" x2="12" y2="15"/></svg>',
    move: '<svg viewBox="0 0 24 24" style="width:12px;height:12px;stroke:currentColor;fill:none;stroke-width:2"><polyline points="16 3 21 3 21 8"/><line x1="21" y1="3" x2="13" y2="11"/><polyline points="8 21 3 21 3 16"/><line x1="3" y1="21" x2="11" y2="13"/></svg>',
    trash: '<svg viewBox="0 0 24 24" style="width:12px;height:12px;stroke:var(--rose);fill:none;stroke-width:2"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 01-2 2H7a2 2 0 01-2-2V6m3 0V4a2 2 0 012-2h4a2 2 0 012 2v2"/></svg>',
    note: '<svg viewBox="0 0 24 24" style="width:12px;height:12px;stroke:currentColor;fill:none;stroke-width:2"><path d="M12 20h9"/><path d="M16.5 3.5a2.12 2.12 0 013 3L7 19l-4 1 1-4L16.5 3.5z"/></svg>',
    activity: '<svg viewBox="0 0 24 24" style="width:14px;height:14px;stroke:#fff;fill:none;stroke-width:2"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></svg>',
};

const WORKFLOW = [
    {key:'CLIENT_CAPTURE',label:'Intake'},{key:'INITIAL_CONTACT',label:'Contact'},
    {key:'CASE_EVALUATION',label:'Evaluate'},{key:'FEE_COLLECTION',label:'Fees'},
    {key:'GROUP_CREATION',label:'Setup'},{key:'MATERIAL_INGESTION',label:'Materials'},
    {key:'DOCUMENT_GENERATION',label:'Drafting'},{key:'CLIENT_APPROVAL',label:'Approval'},
    {key:'FINAL_PDF_SEND',label:'Delivery'},{key:'ARCHIVE_CLOSE',label:'Closed'}
];
const HITL_GATES = {CASE_EVALUATION:'Case evaluation',DOCUMENT_GENERATION:'Document drafting',FINAL_PDF_SEND:'Final delivery'};

function stageClass(state){
    if(!state) return 'stage-default';
    if(state==='CLIENT_CAPTURE'||state==='INITIAL_CONTACT') return 'stage-intake';
    if(state==='CASE_EVALUATION'||state==='FEE_COLLECTION') return 'stage-eval';
    if(state==='DOCUMENT_GENERATION'||state==='CLIENT_APPROVAL') return 'stage-draft';
    if(state==='ARCHIVE_CLOSE') return 'stage-done';
    return 'stage-default';
}
function stageLabel(state){ const w=WORKFLOW.find(s=>s.key===state); return w?w.label:state?.replace(/_/g,' ')||'--'; }

/* ─── State ─── */
const S = { cases:[], jobs:{}, rag:{}, selectedCase:null, feed:[], caseFilter:'', docFilter:'', stageFilter:'', device:{}, uptime:'--', wsCount:0 };

function toast(msg,type='info'){
    const el=document.createElement('div'); el.className='toast '+type; el.textContent=msg;
    document.getElementById('toast-container').appendChild(el);
    setTimeout(()=>el.remove(),4000);
}

function addFeed(text,icon='fi-job'){
    S.feed.unshift({text,icon,time:new Date()});
    if(S.feed.length>50) S.feed.length=50;
    if(location.hash===''||location.hash==='#monitor') renderFeed();
}

/* ─── Router ─── */
const VIEWS = ['monitor','cases','documents','knowledge','settings'];
const VIEW_TITLES = {monitor:'Monitor',cases:'Cases',documents:'Documents',knowledge:'Knowledge Search',settings:'Settings'};

function route(){
    const h = location.hash.replace('#','');
    let view = h || 'monitor';
    let caseId = null;
    
    if (h.startsWith('case/')) {
        view = 'case-workspace';
        caseId = h.split('/')[1];
    } else if (!VIEWS.includes(view)) {
        view = 'monitor';
    }

    document.querySelectorAll('.view').forEach(v=>v.classList.remove('active'));
    document.getElementById('view-'+view)?.classList.add('active');
    
    document.querySelectorAll('.nav-btn').forEach(b=>{
        const bv = b.dataset.view;
        // highlight the nav button if it matches, or if we're in case workspace highlight the cases icon
        b.classList.toggle('active', bv === view || (view === 'case-workspace' && bv === 'cases'));
    });
    
    if (view === 'case-workspace' && caseId) {
        document.getElementById('view-title').textContent = 'Case Workspace';
        renderCaseWorkspace(caseId);
    } else {
        document.getElementById('view-title').textContent = VIEW_TITLES[view]||view;
        renderView(view);
    }
}

function renderView(v){
    if(v==='monitor'){renderKPI();renderCaseGrid();renderFeed();renderJobQueue();}
    if(v==='cases') renderCasesTable();
    if(v==='documents') renderDocsGrid();
    if(v==='settings') renderSettings();
}

/* ─── API ─── */
async function api(path,opts){ return (await fetch(API+path, opts)).json().catch(()=>({})); }

async function refreshAll(){
    try {
        const d = await api('/status');
        if(d.cases) S.cases = d.cases;
        if(d.rag) S.rag = d.rag;
        if(d.uptime) S.uptime = d.uptime;
        if(d.active_ws!==undefined) S.wsCount = d.active_ws;
        if(d.jobs){ const nj={}; (Array.isArray(d.jobs)?d.jobs:Object.values(d.jobs)).forEach(j=>nj[j.id]=j); S.jobs=nj; }
        if(S.selectedCase){ const u=S.cases.find(c=>c.case_id===S.selectedCase.case_id); if(u) S.selectedCase=u; }
        renderView(location.hash.replace('#','')||'monitor');
    } catch(e){}
}

async function fetchFullCase(id){
    try{ S.selectedCase=await api('/cases/'+id); renderSlideOver(); }catch(e){toast('Failed to load case','error');}
}

/* ─── Renderers ─── */
function renderKPI(){
    document.getElementById('kpi-cases').textContent = S.cases.length;
    const jobArr = Object.values(S.jobs);
    const active = jobArr.filter(j=>j.status==='processing'||j.status==='pending');
    document.getElementById('kpi-jobs').textContent = jobArr.length;
    document.getElementById('kpi-jobs-sub').textContent = active.length ? active.length+' running' : 'system idle';
    document.getElementById('kpi-docs').textContent = S.rag?.total_documents ?? S.rag?.document_count ?? 0;
    const up = S.uptime||'--';
    document.getElementById('kpi-uptime').textContent = up.includes('h') ? up.split('.')[0] : up.replace(/\..*/,'');
}

function renderCaseGrid(){
    const el = document.getElementById('monitor-case-grid');
    if(!S.cases.length){ el.innerHTML='<div class="empty"><svg viewBox="0 0 24 24" style="width:48px;height:48px;stroke:var(--text-3);fill:none;stroke-width:1;opacity:.4"><path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2V7z"/></svg><h3>No cases yet</h3><p>Create a case or upload documents to get started.</p></div>'; return; }
    el.innerHTML = '<div class="case-grid">'+S.cases.map(c=>{
        const docs = c.uploaded_documents?.length||0;
        return `<div class="case-card" data-id="${escA(c.case_id)}"><div class="case-card-name">${esc(c.client_name)}</div><div class="case-card-matter">${esc(c.matter_type)}</div><div class="case-card-row"><span class="stage-badge ${stageClass(c.state)}">${stageLabel(c.state)}</span><span class="doc-count">${ICONS.doc} ${docs}</span></div></div>`;
    }).join('')+'</div>';
}

function renderFeed(){
    const el=document.getElementById('monitor-feed');
    if(!S.feed.length){el.innerHTML='<div class="empty"><svg viewBox="0 0 24 24" style="width:48px;height:48px;stroke:var(--text-3);fill:none;stroke-width:1;opacity:.4"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></svg><h3>No activity yet</h3><p>Agent actions will appear here in real-time.</p></div>';return;}
    el.innerHTML = S.feed.slice(0,30).map(f=>`<div class="feed-item"><div class="feed-icon ${f.icon}">${ICONS.activity}</div><div class="feed-text">${f.text}</div><div class="feed-time">${f.time.toLocaleTimeString()}</div></div>`).join('');
}

function renderJobQueue(){
    const el=document.getElementById('monitor-jobs');
    const jobs=Object.values(S.jobs);
    if(!jobs.length){el.innerHTML='<div style="padding:12px;text-align:center;font-size:10px;color:var(--text-3);font-weight:600">No active jobs</div>';return;}
    el.innerHTML = jobs.map(j=>`<div class="job-row"><span class="job-type">${esc(j.type)}</span><div class="job-bar"><div class="job-bar-fill" style="width:${j.progress||0}%"></div></div><span class="job-status ${j.status}">${j.status} ${j.progress||0}%</span></div>`).join('');
}

function renderCasesTable(){
    const el=document.getElementById('cases-tbody');
    let list=S.cases;
    const q=S.caseFilter.toLowerCase();
    if(q) list=list.filter(c=>c.client_name.toLowerCase().includes(q)||c.case_id.toLowerCase().includes(q)||c.matter_type.toLowerCase().includes(q));
    if(S.stageFilter) list=list.filter(c=>c.state===S.stageFilter);
    if(!list.length){el.innerHTML='<tr><td colspan="6" style="text-align:center;padding:40px;color:var(--text-3)">No cases found</td></tr>';return;}
    el.innerHTML = list.map(c=>{
        const docs=c.uploaded_documents?.length||0;
        const dotColor = c.state==='ARCHIVE_CLOSE'?'var(--emerald)':c.state?.includes('EVALUATION')?'var(--amber)':'var(--accent)';
        return `<tr data-id="${escA(c.case_id)}"><td><span class="status-dot" style="background:${dotColor}"></span></td><td style="font-weight:600;color:var(--text-0)">${esc(c.client_name)}</td><td>${esc(c.matter_type)}</td><td><span class="stage-badge ${stageClass(c.state)}">${stageLabel(c.state)}</span></td><td>${docs}</td><td style="font-family:var(--font-mono);font-size:11px">${new Date(c.created_at).toLocaleDateString()}</td></tr>`;
    }).join('');
}

function renderDocsGrid(){
    const el=document.getElementById('docs-grid');
    const allDocs=[];
    S.cases.forEach(c=>{(c.uploaded_documents||[]).forEach(d=>allDocs.push({name:d,caseId:c.case_id,client:c.client_name}));});
    const q = S.docFilter.toLowerCase();
    const filtered = q ? allDocs.filter(d=>d.name.toLowerCase().includes(q)||d.client.toLowerCase().includes(q)) : allDocs;
    if(!filtered.length){el.innerHTML='<div class="empty" style="grid-column:1/-1"><svg viewBox="0 0 24 24" style="width:48px;height:48px;stroke:var(--text-3);fill:none;stroke-width:1;opacity:.4"><path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8l-6-6z"/><polyline points="14 2 14 8 20 8"/></svg><h3>No documents</h3><p>Upload files to index them into the knowledge base.</p></div>';return;}
    el.innerHTML = filtered.map(d=>`<div class="doc-card" data-doc="${escA(d.name)}" data-case="${escA(d.caseId)}"><div class="doc-card-icon"><svg viewBox="0 0 24 24" style="width:18px;height:18px;stroke:var(--cyan);fill:none;stroke-width:1.8"><path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8l-6-6z"/><polyline points="14 2 14 8 20 8"/></svg></div><div class="doc-card-name" title="${escA(d.name)}">${esc(d.name)}</div><div class="doc-card-meta">${esc(d.client)}</div></div>`).join('');
}

function renderSettings(){
    const d=S.device;
    document.getElementById('settings-system').innerHTML=[
        row('Platform',d.platform_id||'--'),row('Memory',(d.memory_mb||'--')+' MB'),
        row('Apple Silicon',d.is_apple_silicon?'Yes':'No'),row('LLM Backend',d.llm_backend||'--'),
        row('LLM Model',d.llm_model||'--'),row('OCR Model',d.ocr_model||'--'),
        row('Max Concurrent',d.max_concurrent??'--'),
    ].join('');
    document.getElementById('settings-conn').innerHTML=[row('WebSocket',_wsConnected?'Connected':'Disconnected'),row('Active Clients',S.wsCount),row('Uptime',S.uptime||'--')].join('');
    const rc=S.rag;
    document.getElementById('settings-rag').innerHTML=[row('Documents',rc.total_documents??rc.document_count??0),row('Chunks',rc.total_chunks??rc.chunk_count??0)].join('');
}
function row(l,v){return `<div class="setting-row"><span class="setting-label">${esc(l)}</span><span class="setting-value">${esc(String(v))}</span></div>`;}

/* ─── Slide-over ─── */
function openSlideOver(caseId){
    fetchFullCase(caseId);
    document.getElementById('so-backdrop').classList.add('open');
    document.getElementById('slideover').classList.add('open');
}
function closeSlideOver(){
    document.getElementById('so-backdrop').classList.remove('open');
    document.getElementById('slideover').classList.remove('open');
    S.selectedCase=null;
}
function renderSlideOver(){
    const c=S.selectedCase; if(!c) return;
    document.getElementById('so-title').textContent=c.client_name;
    const curIdx=WORKFLOW.findIndex(w=>w.key===c.state);

    let hitlHtml='';
    const nextIdx=curIdx+1;
    if(nextIdx<WORKFLOW.length){
        const gate=WORKFLOW[nextIdx].key;
        if(HITL_GATES[gate]){
            const approvals=c.hitl_approvals||{};
            if(approvals[gate]!==true){
                const status=approvals[gate]===false?'Previously rejected':'Awaiting approval';
                hitlHtml=`<div class="hitl-box"><div class="hitl-title">Human Review Required</div><div class="hitl-desc">${status}: ${HITL_GATES[gate]}</div><div class="hitl-actions"><input class="hitl-input" id="hitl-reason" placeholder="Audit note (optional)"><button class="btn-approve" onclick="submitHITL(true)">Approve</button><button class="btn-reject" onclick="submitHITL(false)">Reject</button></div></div>`;
            }
        }
    }

    let wfHtml='<div class="wf-track">';
    WORKFLOW.forEach((w,i)=>{
        if(i>0) wfHtml+=`<div class="wf-line${i<=curIdx?' done':''}"></div>`;
        const cls=i<curIdx?'done':i===curIdx?'current':'';
        wfHtml+=`<div class="wf-step"><div class="wf-dot ${cls}"></div><div class="wf-label ${cls}">${w.label}</div></div>`;
    });
    wfHtml+='</div>';

    const docs=c.uploaded_documents||[];
    let docsHtml=docs.length?docs.map(d=>`<div style="display:flex;align-items:center;gap:8px;padding:8px;border-radius:var(--radius-xs);background:var(--bg-2);margin-bottom:6px;cursor:pointer" onclick="viewDocument('${escA(d)}')"><div style="width:28px;height:28px;border-radius:6px;background:var(--bg-3);display:flex;align-items:center;justify-content:center">${ICONS.doc}</div><span style="font-size:11px;flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">${esc(d)}</span><div style="display:flex;gap:4px;flex-shrink:0" onclick="event.stopPropagation()"><button type="button" title="Move to another case" onclick="openReassignModal('${escA(c.case_id)}','${escA(d)}')" style="width:24px;height:24px;border-radius:6px;display:flex;align-items:center;justify-content:center;opacity:.5;transition:all .15s" onmouseover="this.style.opacity=1;this.style.background='var(--accent-glow)'" onmouseout="this.style.opacity=.5;this.style.background='none'">${ICONS.move}</button><button type="button" title="Delete" onclick="deleteDocument('${escA(c.case_id)}','${escA(d)}')" style="width:24px;height:24px;border-radius:6px;display:flex;align-items:center;justify-content:center;opacity:.4;transition:all .15s" onmouseover="this.style.opacity=1;this.style.background='var(--rose-glow)'" onmouseout="this.style.opacity=.4;this.style.background='none'">${ICONS.trash}</button></div></div>`).join(''):'<div style="padding:16px;text-align:center;font-size:10px;color:var(--text-3)">No documents</div>';
    docsHtml+=`<button class="filter-btn" style="margin-top:8px;width:100%;justify-content:center" onclick="document.getElementById('so-file-input').click()">${ICONS.upload} Upload to Case</button><input type="file" id="so-file-input" multiple hidden onchange="uploadToCase(event)">`;

    const summary = c.ai_case_summary || 'No summary generated yet.';
    const notes=c.notes||[];
    let notesHtml=notes.slice().reverse().map(n=>`<div class="note-item"><div class="note-text">${esc(n.text)}</div><div class="note-time">${new Date(n.timestamp).toLocaleString()}</div></div>`).join('');
    if(!notesHtml) notesHtml='<div style="font-size:10px;color:var(--text-3);text-align:center;padding:8px">No notes</div>';

    document.getElementById('so-body').innerHTML=`
        ${hitlHtml}
        <div class="so-section"><div class="so-section-title">${ICONS.activity} Workflow</div>${wfHtml}</div>
        <div class="so-section"><div class="so-section-title" style="display:flex;justify-content:space-between;width:100%"><span>${ICONS.doc} Documents (${docs.length})</span></div>${docsHtml}</div>
        <div class="so-section"><div class="so-section-title" style="display:flex;justify-content:space-between;width:100%"><span>AI Summary</span><button class="panel-action" onclick="generateSummary()">Refresh</button></div><div style="font-size:11px;color:var(--text-1);line-height:1.6;padding:12px;border-radius:var(--radius-xs);background:var(--bg-2);white-space:pre-wrap" id="so-summary">${esc(summary)}</div></div>
        <div class="so-section"><div class="so-section-title" style="display:flex;justify-content:space-between;width:100%"><span>📝 Document Draft</span></div><button class="primary-btn" style="width:100%;justify-content:center" onclick="openEditor('${escA(c.case_id)}','${escA(c.client_name)} — ${escA(c.matter_type)}')"><svg viewBox="0 0 24 24" style="width:14px;height:14px;stroke:currentColor;fill:none;stroke-width:2"><path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8l-6-6z"/><polyline points="14 2 14 8 20 8"/><line x1="16" y1="13" x2="8" y2="13"/><line x1="16" y1="17" x2="8" y2="17"/></svg> Open Draft Editor</button></div>
        <div class="so-section"><div class="so-section-title" style="display:flex;justify-content:space-between;width:100%"><span>${ICONS.note} Notes</span><button class="panel-action" onclick="openModal('modal-note')">+ Add</button></div>${notesHtml}</div>
        <div class="so-section"><div class="so-section-title">Details</div>${row('Case ID',c.case_id)}${row('Matter',c.matter_type)}${row('Channel',c.source_channel||'Direct')}${row('Created',new Date(c.created_at).toLocaleDateString())}</div>
    `;
}

async function renderCaseWorkspace(caseId) {
    if (!S.cases.find(c => c.case_id === caseId)) {
        await fetchFullCase(caseId);
    } else {
        fetchFullCase(caseId);
    }
    const c = S.selectedCase || S.cases.find(co => co.case_id === caseId);
    if (!c) return;

    document.getElementById('ws-case-name').textContent = c.client_name + ' — ' + c.matter_type;
    document.getElementById('ws-case-stage').textContent = stageLabel(c.state);
    document.getElementById('ws-case-stage').className = 'stage-badge ' + stageClass(c.state);

    const curIdx = WORKFLOW.findIndex(w => w.key === c.state);
    
    let hitlHtml = '';
    const nextIdx = curIdx + 1;
    if (nextIdx < WORKFLOW.length) {
        const gate = WORKFLOW[nextIdx].key;
        if (HITL_GATES[gate]) {
            const approvals = c.hitl_approvals || {};
            if(approvals[gate]!==true){
                const status = approvals[gate]===false?'Previously rejected':'Awaiting approval';
                hitlHtml = `<div class="hitl-box"><div class="hitl-title">Human Review Required</div><div class="hitl-desc">${status}: ${HITL_GATES[gate]}</div><div class="hitl-actions"><input class="hitl-input" id="ws-hitl-reason" placeholder="Audit note (optional)"><button class="btn-approve" onclick="submitHITL(true)">Approve</button><button class="btn-reject" onclick="submitHITL(false)">Reject</button></div></div>`;
            }
        }
    }

    let wfHtml = '<div class="wf-track">';
    WORKFLOW.forEach((w,i)=>{
        if(i>0) wfHtml+=`<div class="wf-line${i<=curIdx?' done':''}"></div>`;
        const cls=i<curIdx?'done':i===curIdx?'current':'';
        wfHtml+=`<div class="wf-step"><div class="wf-dot ${cls}"></div><div class="wf-label ${cls}">${w.label}</div></div>`;
    });
    wfHtml += '</div>';

    const notes = c.notes || [];
    let notesHtml = notes.slice().reverse().map(n=>`<div class="note-item"><div class="note-text">${esc(n.text)}</div><div class="note-time">${new Date(n.timestamp).toLocaleString()}</div></div>`).join('');
    if(!notesHtml) notesHtml='<div style="font-size:10px;color:var(--text-3);text-align:center;padding:8px">No notes</div>';

    const summary = c.ai_case_summary || 'No summary generated yet.';
    const docs = c.uploaded_documents || [];
    let docsHtml = docs.length ? docs.map(d=>`<div style="display:flex;align-items:center;padding:12px;background:var(--bg-0);border:1px solid var(--glass-border);border-radius:var(--radius-sm);margin-bottom:8px;cursor:pointer;transition:all 0.2s;" onmouseover="this.style.borderColor='var(--accent)'" onmouseout="this.style.borderColor='var(--glass-border)'" onclick="viewDocument('${escA(d)}')"><div style="width:36px;height:36px;border-radius:10px;background:var(--bg-3);display:flex;align-items:center;justify-content:center;margin-right:12px;">${ICONS.doc}</div><div style="flex:1;min-width:0;"><div style="font-size:12px;font-weight:600;color:var(--text-0);margin-bottom:4px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;">${esc(d)}</div><div style="font-size:10px;color:var(--text-2);">Uploaded document</div></div><div style="display:flex;gap:6px;flex-shrink:0" onclick="event.stopPropagation()"><button type="button" title="Move to another case" onclick="openReassignModal('${escA(c.case_id)}','${escA(d)}')" style="padding:6px;border-radius:6px;display:flex;align-items:center;justify-content:center;color:var(--text-2);" onmouseover="this.style.color='var(--accent)'" onmouseout="this.style.color='var(--text-2)'">${ICONS.move}</button><button type="button" title="Delete" onclick="deleteDocument('${escA(c.case_id)}','${escA(d)}')" style="padding:6px;border-radius:6px;display:flex;align-items:center;justify-content:center;color:var(--text-2);" onmouseover="this.style.color='var(--rose)'" onmouseout="this.style.color='var(--text-2)'">${ICONS.trash}</button></div></div>`).join('') : '<div style="padding:24px;text-align:center;font-size:12px;color:var(--text-3);background:var(--glass);border-radius:var(--radius-sm);">No documents uploaded yet.</div>';

    const elWsId = document.getElementById('ws-case-id');
    if (elWsId) elWsId.textContent = c.case_id;

    document.getElementById('ws-main-grid').innerHTML = `
        ${hitlHtml}
        
        <!-- Workflow Tracker -->
        <div style="grid-column: span 8; background:var(--bg-2); border:1px solid var(--glass-border); border-radius:16px; padding:24px; display:flex; flex-direction:column; justify-content:center; box-shadow:0 10px 30px rgba(0,0,0,0.15); min-height:160px;">
            <div style="font-size:10px; font-weight:700; color:var(--text-2); text-transform:uppercase; letter-spacing:1px; margin-bottom:24px; display:flex; align-items:center; gap:8px;">${ICONS.activity} Workflow Status</div>
            ${wfHtml}
        </div>
        
        <!-- Smart Draft Banner -->
        <div style="grid-column: span 4; background: linear-gradient(135deg, var(--accent), var(--violet)); border-radius:16px; padding:24px; position:relative; overflow:hidden; box-shadow:0 10px 30px rgba(0,0,0,0.2); color:white; display:flex; flex-direction:column; justify-content:center; min-height:160px;">
            <div style="position:absolute; top:-40px; right:-40px; width:150px; height:150px; background:radial-gradient(circle, rgba(255,255,255,0.2) 0%, transparent 70%); border-radius:50%;"></div>
            <div style="font-size:11px; font-weight:800; text-transform:uppercase; letter-spacing:1px; margin-bottom:12px; z-index:1; display:flex; align-items:center; gap:8px;">✨ Smart Legal Drafter</div>
            <p style="font-size:12px; opacity:0.9; margin-bottom:20px; line-height:1.5; z-index:1; padding-right:12px;">Auto-generate structured drafts directly from your case evidence.</p>
            <button class="primary-btn" style="background:white; color:var(--accent); font-weight:600; font-size:12px; justify-content:center; padding:10px; border-radius:8px; z-index:1; border:none; box-shadow:0 4px 12px rgba(0,0,0,0.3);" onclick="openEditor('${escA(c.case_id)}','${escA(c.client_name)} — ${escA(c.matter_type)}')">
                <svg viewBox="0 0 24 24" style="width:14px;height:14px;stroke:currentColor;fill:none;stroke-width:2;margin-right:6px;"><path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8l-6-6z"/><polyline points="14 2 14 8 20 8"/><line x1="16" y1="13" x2="8" y2="13"/><line x1="16" y1="17" x2="8" y2="17"/></svg>
                Launch Editor
            </button>
        </div>
        
        <!-- Left: Case Info & Notes (Span 3) -->
        <div style="grid-column: span 3; display:flex; flex-direction:column; gap:24px;">
            <div style="background:var(--bg-2); border:1px solid var(--glass-border); border-radius:16px; padding:24px; box-shadow:0 10px 30px rgba(0,0,0,0.15);">
               <div style="font-size:10px; font-weight:700; color:var(--text-2); text-transform:uppercase; letter-spacing:1px; margin-bottom:20px;">Case Details</div>
               ${row('Matter',c.matter_type)}${row('Channel',c.source_channel||'Direct')}${row('Created',new Date(c.created_at).toLocaleDateString())}
            </div>
            
            <div style="flex:1; background:var(--bg-2); border:1px solid var(--glass-border); border-radius:16px; padding:24px; box-shadow:0 10px 30px rgba(0,0,0,0.15); display:flex; flex-direction:column; min-height:300px;">
                <div style="font-size:10px; font-weight:700; color:var(--text-2); text-transform:uppercase; letter-spacing:1px; margin-bottom:16px; display:flex; justify-content:space-between; align-items:center;">
                    <span>${ICONS.note} Notes</span>
                    <button class="filter-btn" style="padding:4px 8px; font-size:10px;" onclick="openModal('modal-note')">+ Add</button>
                </div>
                <div style="flex:1; overflow-y:auto; margin: -10px; padding: 10px;">${notesHtml}</div>
            </div>
        </div>
        
        <!-- Center: Documents (Span 4) -->
        <div style="grid-column: span 4; background:var(--bg-2); border:1px solid var(--glass-border); border-radius:16px; padding:24px; box-shadow:0 10px 30px rgba(0,0,0,0.15); display:flex; flex-direction:column; max-height: 520px;">
            <div style="font-size:10px; font-weight:700; color:var(--text-2); text-transform:uppercase; letter-spacing:1px; margin-bottom:20px; display:flex; justify-content:space-between; align-items:center;">
                <span>${ICONS.doc} Documents (${docs.length})</span>
                <div>
                   <button class="filter-btn" style="padding:4px 8px; font-size:10px;" onclick="document.getElementById('ws-file-input').click()">${ICONS.upload} Upload</button>
                   <input type="file" id="ws-file-input" multiple hidden onchange="uploadToCase(event)">
                </div>
            </div>
            <div style="flex:1; overflow-y:auto; padding-right:8px;">${docsHtml}</div>
        </div>
        
        <!-- Right: AI Summary (Span 5) -->
        <div style="grid-column: span 5; background:var(--bg-2); border:1px solid var(--glass-border); border-radius:16px; padding:24px; box-shadow:0 10px 30px rgba(0,0,0,0.15); display:flex; flex-direction:column; max-height: 520px;">
             <div style="font-size:10px; font-weight:700; color:var(--text-2); text-transform:uppercase; letter-spacing:1px; margin-bottom:20px; display:flex; justify-content:space-between; align-items:center;">
                <span style="display:flex; align-items:center; gap:6px;">✨ Case Intelligence Summary</span>
                <button class="filter-btn" style="padding:4px 8px; font-size:10px;" onclick="generateSummary()">Refresh</button>
            </div>
            <div style="font-size:12px; color:var(--text-1); line-height:1.7; white-space:pre-wrap; background:var(--bg-0); padding:20px; border-radius:12px; border:1px solid var(--glass-border); flex:1; overflow-y:auto;">${esc(summary)}</div>
        </div>
    `;
}

/* ─── Document Viewer ─── */
let _viewerUrl='';
function viewDocument(filename){
    const url=`${API}/documents/${encodeURIComponent(filename)}/view`;
    _viewerUrl=url;
    document.getElementById('viewer-title').textContent=filename;
    const body=document.getElementById('viewer-body');
    const lower=filename.toLowerCase();
    if(/\.(png|jpe?g|gif|webp|bmp|tiff?)$/i.test(lower)){body.innerHTML=`<img src="${url}" alt="Preview">`;}
    else if(/\.(docx|txt|md|csv)$/i.test(lower)){body.innerHTML='<pre>Loading...</pre>';fetch(`${API}/documents/${encodeURIComponent(filename)}/content`).then(r=>r.json()).then(d=>{body.querySelector('pre').textContent=d.content||'No content';}).catch(()=>{body.querySelector('pre').textContent='Failed to load.';});}
    else{body.innerHTML=`<iframe src="${url}"></iframe>`;}
    document.getElementById('doc-viewer').classList.add('open');
}
function closeViewer(){document.getElementById('doc-viewer').classList.remove('open');document.getElementById('viewer-body').innerHTML='';}

/* ─── Modals ─── */
function openModal(id){document.getElementById(id).classList.add('open');}
function closeModal(id){document.getElementById(id).classList.remove('open');}

/* ─── Actions ─── */
async function createCase(){
    const name=document.getElementById('nc-name').value.trim();
    if(!name) return toast('Client name required','error');
    try{
        const d=await api('/cases/create',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({client_name:name,matter_type:document.getElementById('nc-matter').value,initial_msg:document.getElementById('nc-msg').value})});
        toast('Case created','success'); closeModal('modal-new-case');
        document.getElementById('nc-name').value=''; document.getElementById('nc-msg').value='';
        addFeed(`<strong>Case created</strong> for ${esc(name)}`,'fi-case');
        if(d.case_id) openSlideOver(d.case_id);
        refreshAll();
    }catch(e){toast('Create failed','error');}
}

async function updateCase(){
    if(!S.selectedCase) return;
    const name=document.getElementById('ec-name').value.trim();
    if(!name) return toast('Name required','error');
    try{
        await api('/cases/'+S.selectedCase.case_id,{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({client_name:name,matter_type:document.getElementById('ec-matter').value})});
        toast('Case updated','success'); closeModal('modal-edit-case');
        addFeed(`<strong>Case updated</strong>: ${esc(name)}`,'fi-case');
        fetchFullCase(S.selectedCase.case_id); refreshAll();
    }catch(e){toast('Update failed','error');}
}

async function deleteCase(){
    if(!S.selectedCase||!confirm('Delete case for '+S.selectedCase.client_name+'?')) return;
    try{
        await api('/cases/'+S.selectedCase.case_id+'/delete',{method:'POST'});
        toast('Case deleted','success'); addFeed(`<strong>Case deleted</strong>: ${esc(S.selectedCase.client_name)}`,'fi-warn');
        closeSlideOver(); refreshAll();
    }catch(e){toast('Delete failed','error');}
}

async function advanceCase(){
    if(!S.selectedCase) return;
    try{
        const r=await fetch(API+'/cases/'+S.selectedCase.case_id+'/advance',{method:'POST'});
        if(!r.ok){const d=await r.json().catch(()=>({}));toast(d.error||'Cannot advance','error');return;}
        toast('Advanced to next step','success'); addFeed(`<strong>Case advanced</strong>: ${esc(S.selectedCase.client_name)}`,'fi-case');
        fetchFullCase(S.selectedCase.case_id); refreshAll();
    }catch(e){toast('Advance failed','error');}
}

async function submitHITL(approved){
    if(!S.selectedCase) return;
    const curIdx=WORKFLOW.findIndex(w=>w.key===S.selectedCase.state);
    const gate=WORKFLOW[curIdx+1]?.key;
    if(!gate||!HITL_GATES[gate]) return toast('No gate active','error');
    const reason=document.getElementById('hitl-reason')?.value.trim()||(approved?'Approved':'Rejected');
    try{
        const r=await fetch(API+'/cases/'+S.selectedCase.case_id+'/approve',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({state:gate,approved,reason})});
        if(!r.ok){const d=await r.json().catch(()=>({}));toast(d.error||'Review failed','error');return;}
        toast(approved?'Approved':'Rejected',approved?'success':'info');
        addFeed(`<strong>HITL ${approved?'approved':'rejected'}</strong>: ${esc(S.selectedCase.client_name)}`,'fi-warn');
        fetchFullCase(S.selectedCase.case_id); refreshAll();
    }catch(e){toast('Review failed','error');}
}

async function addNote(){
    if(!S.selectedCase) return;
    const text=document.getElementById('note-text').value.trim();
    if(!text) return;
    try{
        await api('/cases/'+S.selectedCase.case_id+'/notes',{method:'POST',body:JSON.stringify({text})});
        toast('Note added','success'); closeModal('modal-note');
        document.getElementById('note-text').value='';
        fetchFullCase(S.selectedCase.case_id);
    }catch(e){toast('Failed','error');}
}

async function generateSummary(){
    if(!S.selectedCase) return;
    const el=document.getElementById('so-summary');
    if(el) el.textContent='AI analyzing documents...';
    try{
        const d=await api('/cases/'+S.selectedCase.case_id+'/summarize',{method:'POST'});
        if(el) el.textContent=d.summary||'No summary returned.';
        addFeed(`<strong>Summary generated</strong> for ${esc(S.selectedCase.client_name)}`,'fi-doc');
    }catch(e){if(el) el.textContent='Summary failed.';}
}

async function deleteDocument(caseId,filename){
    if(!confirm('Delete '+filename+'?')) return;
    try{
        await fetch(API+'/cases/'+caseId+'/documents/'+encodeURIComponent(filename),{method:'DELETE'});
        toast('Document deleted','success'); addFeed(`<strong>Doc deleted</strong>: ${esc(filename)}`,'fi-warn');
        fetchFullCase(caseId); refreshAll();
    }catch(e){toast('Delete failed','error');}
}

function openReassignModal(caseId,filename){
    document.getElementById('reassign-source-case').value=caseId;
    document.getElementById('reassign-filename').value=filename;
    const sel=document.getElementById('reassign-target');
    sel.innerHTML='';
    const others=(S.cases||[]).filter(c=>c.case_id!==caseId);
    if(!others.length){ toast('No other case to move to','error'); return; }
    others.forEach(c=>{
        const o=document.createElement('option');
        o.value=c.case_id;
        o.textContent=c.client_name+' — '+c.case_id;
        sel.appendChild(o);
    });
    openModal('modal-reassign-doc');
}

async function submitReassign(){
    const tgt=document.getElementById('reassign-target').value;
    const src=document.getElementById('reassign-source-case').value;
    const fn=document.getElementById('reassign-filename').value;
    if(!tgt||!src||!fn){ toast('Missing move parameters','error'); return; }
    try{
        const r=await fetch(API+'/cases/'+encodeURIComponent(src)+'/documents/reassign',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({filename:fn,target_case_id:tgt})});
        const d=await r.json().catch(()=>({}));
        if(!r.ok){ toast(d.error||'Move failed','error'); return; }
        toast('Document moved','success');
        addFeed(`<strong>Doc moved</strong>: ${esc(fn)} → case ${esc(tgt)}`,'fi-doc');
        closeModal('modal-reassign-doc');
        await refreshAll();
        if(S.selectedCase && S.selectedCase.case_id===src){ await fetchFullCase(src); }
        const h=location.hash.replace('#','');
        if(h.startsWith('case/')){ await renderCaseWorkspace(h.slice(5)); }
    }catch(e){ toast('Move failed','error'); }
}

async function uploadFiles(input){
    const files=input.files; if(!files||!files.length) return;
    const fd=new FormData();
    for(let i=0;i<files.length;i++) fd.append('files',files[i]);
    if(S.selectedCase) fd.append('case_id',S.selectedCase.case_id);
    try{
        const r=await fetch(API+'/upload/batch',{method:'POST',body:fd});
        const d=await r.json().catch(()=>({}));
        if(!r.ok){toast(d.error||'Upload failed','error');return;}
        toast('Processing '+files.length+' files...','info');
        addFeed(`<strong>Upload started</strong>: ${files.length} files`,'fi-doc');
    }catch(e){toast('Upload failed','error');}
    input.value='';
}
function uploadToCase(e){uploadFiles(e.target);}

async function searchRAG(){
    const q=document.getElementById('rag-input').value.trim();
    if(!q) return;
    const el=document.getElementById('rag-results');
    el.innerHTML='<div style="display:flex;justify-content:center;padding:40px"><div style="width:24px;height:24px;border:2px solid var(--accent);border-top-color:transparent;border-radius:50%;animation:spin 1s linear infinite"></div></div><style>@keyframes spin{to{transform:rotate(360deg)}}</style>';
    try{
        const d=await api('/rag/search',{method:'POST',body:JSON.stringify({query:q,k:5})});
        if(!d.results?.length){el.innerHTML='<div class="empty"><h3>No matches</h3><p>Try a different query.</p></div>';return;}
        el.innerHTML=d.results.map(r=>`<div class="know-result"><div class="know-result-file"><span>${esc(r.filename)}</span><span style="color:var(--text-3)">${r.score?.toFixed(3)||''}</span></div><div class="know-result-text">${esc(r.chunk)}</div></div>`).join('');
    }catch(e){el.innerHTML='<div class="empty"><h3>Search failed</h3></div>';}
}

function openEditModal(){
    if(!S.selectedCase) return;
    document.getElementById('ec-name').value=S.selectedCase.client_name;
    document.getElementById('ec-matter').value=S.selectedCase.matter_type;
    openModal('modal-edit-case');
}

/* ─── Event Bindings ─── */
document.querySelectorAll('.nav-btn[data-view]').forEach(b=>b.addEventListener('click',()=>{location.hash=b.dataset.view;}));
window.addEventListener('hashchange', route);
document.getElementById('btn-upload-global').addEventListener('click',()=>document.getElementById('global-file-input').click());
document.getElementById('global-file-input').addEventListener('change',e=>uploadFiles(e.target));
document.getElementById('btn-new-case').addEventListener('click',()=>openModal('modal-new-case'));
document.getElementById('btn-view-all-cases').addEventListener('click',()=>{location.hash='cases';});
document.getElementById('monitor-case-grid').addEventListener('click',e=>{const card=e.target.closest('.case-card');if(card) openSlideOver(card.dataset.id);});
document.getElementById('cases-search').addEventListener('input',e=>{S.caseFilter=e.target.value;renderCasesTable();});
document.getElementById('cases-filter-stage').addEventListener('change',e=>{S.stageFilter=e.target.value;renderCasesTable();});
document.getElementById('btn-new-case-2').addEventListener('click',()=>openModal('modal-new-case'));
document.getElementById('cases-tbody').addEventListener('click',e=>{
    const tr=e.target.closest('tr[data-id]');
    if(tr) location.hash = 'case/' + tr.dataset.id;
});
const stageSelect=document.getElementById('cases-filter-stage');
WORKFLOW.forEach(w=>{const o=document.createElement('option');o.value=w.key;o.textContent=w.label;stageSelect.appendChild(o);});
document.getElementById('docs-search').addEventListener('input',e=>{S.docFilter=e.target.value;renderDocsGrid();});
document.getElementById('btn-upload-docs').addEventListener('click',()=>document.getElementById('docs-file-input').click());
document.getElementById('docs-file-input').addEventListener('change',e=>uploadFiles(e.target));
document.getElementById('docs-grid').addEventListener('click',e=>{const card=e.target.closest('.doc-card');if(card) viewDocument(card.dataset.doc);});
document.getElementById('btn-rag-search').addEventListener('click',searchRAG);
document.getElementById('rag-input').addEventListener('keydown',e=>{if(e.key==='Enter') searchRAG();});
document.getElementById('so-close').addEventListener('click',closeSlideOver);
document.getElementById('so-backdrop').addEventListener('click',closeSlideOver);
document.getElementById('so-advance').addEventListener('click',advanceCase);
document.getElementById('so-edit').addEventListener('click',openEditModal);
document.getElementById('so-delete').addEventListener('click',deleteCase);
document.getElementById('nc-submit').addEventListener('click',createCase);
document.getElementById('nc-cancel').addEventListener('click',()=>closeModal('modal-new-case'));
document.getElementById('ec-submit').addEventListener('click',updateCase);
document.getElementById('ec-cancel').addEventListener('click',()=>closeModal('modal-edit-case'));
document.getElementById('note-submit').addEventListener('click',addNote);
document.getElementById('note-cancel').addEventListener('click',()=>closeModal('modal-note'));
document.getElementById('reassign-submit').addEventListener('click',submitReassign);
document.getElementById('reassign-cancel').addEventListener('click',()=>closeModal('modal-reassign-doc'));
document.getElementById('viewer-close').addEventListener('click',closeViewer);
document.getElementById('viewer-newtab').addEventListener('click',()=>{if(_viewerUrl) window.open(_viewerUrl,'_blank');});
document.querySelectorAll('.modal-backdrop').forEach(m=>m.addEventListener('click',e=>{if(e.target===m) m.classList.remove('open');}));

/* ─── WebSocket ─── */
let _wsBackoff=1000, _wsConnected=false;
function connectWS(){
    const ws=new WebSocket((location.protocol==='https:'?'wss:':'ws:')+'//'+location.host+'/ws');
    ws.onopen=()=>{_wsConnected=true;_wsBackoff=1000;updConn('live','Connected');refreshAll();addFeed('<strong>WebSocket connected</strong>','fi-case');};
    ws.onmessage=e=>{try{const d=JSON.parse(e.data);
        if(d.jobs){(Array.isArray(d.jobs)?d.jobs:Object.values(d.jobs)).forEach(j=>{const old=S.jobs[j.id];if(!old&&j.status!=='completed') addFeed(`<strong>Job started</strong>: ${esc(j.type)}`,'fi-job');else if(old&&old.status!=='completed'&&j.status==='completed') addFeed(`<strong>Job completed</strong>: ${esc(j.type)}`,'fi-case');else if(old&&old.status!=='failed'&&j.status==='failed') addFeed(`<strong>Job failed</strong>: ${esc(j.type)}`,'fi-err');});}
        if(d.cases) S.cases=d.cases;
        if(d.jobs){const nj={};(Array.isArray(d.jobs)?d.jobs:Object.values(d.jobs)).forEach(j=>nj[j.id]=j);S.jobs=nj;}
        if(d.rag) S.rag=d.rag;
        if(S.selectedCase){const u=S.cases.find(c=>c.case_id===S.selectedCase.case_id);if(u){S.selectedCase=u;renderSlideOver();}}
        renderView(location.hash.replace('#','')||'monitor');
    }catch(err){}};
    ws.onerror=()=>updConn('warn','Error');
    ws.onclose=()=>{_wsConnected=false;updConn('dead','Reconnecting');pollHTTP();setTimeout(connectWS,_wsBackoff);_wsBackoff=Math.min(30000,Math.floor(_wsBackoff*1.5));};
}
function updConn(cls,txt){document.getElementById('conn-dot').className='conn-dot '+cls;document.getElementById('conn-text').textContent=txt;}
async function pollHTTP(){if(_wsConnected) return;try{const r=await fetch(API+'/status');if(r.ok){const d=await r.json();if(d.cases)S.cases=d.cases;if(d.rag)S.rag=d.rag;if(d.uptime)S.uptime=d.uptime;updConn('warn','HTTP Polling');renderView(location.hash.replace('#','')||'monitor');}}catch(e){}}
setInterval(pollHTTP,4000);

/* ─── Init ─── */
fetch(API+'/device').then(r=>r.json()).then(d=>{S.device=d;}).catch(()=>{});
connectWS();
route();
refreshAll();
