(() => {
 'use strict';
 const page = document.querySelector('.marketing');
 if (!page || !/^\/@\/($|hosts\/?$|records|certs|logs|security|auth\/passkeys)/.test(location.pathname)) return;
 const tables = [...page.querySelectorAll('table')];
 const cards = page.querySelector('.live-items');
 if (!tables.length && !cards) return;
 page.classList.add('list-page');
 const states=[];const collator=new Intl.Collator(undefined,{numeric:true,sensitivity:'base'});
 const text=node=>(node?.textContent || '').trim();
 const searchCache=new WeakMap();
 const button=(label,title)=>{const b=document.createElement('button');b.type='button';b.className='btn btn-outline-secondary btn-sm';b.textContent=label;if(title)b.setAttribute('aria-label',title);return b;};
    function addressKey(address) {
        if (!address) return '';
        if (!address.includes(':')) {
            return '4:' + address.split('.').map(n => Number(n).toString(16).padStart(2, '0')).join('');
        }
        let value = address.toLowerCase();
        if (value.includes('.')) {
            const split = value.lastIndexOf(':');
            const bytes = value.slice(split + 1).split('.').map(Number);
            value = value.slice(0, split + 1) + ((bytes[0] << 8) | bytes[1]).toString(16)
                + ':' + ((bytes[2] << 8) | bytes[3]).toString(16);
        }
        const halves = value.split('::');
        const left = halves[0] ? halves[0].split(':') : [];
        const right = halves.length > 1 && halves[1] ? halves[1].split(':') : [];
        const groups = halves.length > 1
            ? [...left, ...Array(8 - left.length - right.length).fill('0'), ...right]
            : left;
        return '6:' + groups.map(n => n.padStart(4, '0')).join('');
    }


 function value(row,index,state) {
  if(state.cards)return row.dataset[state.fields[index]]||'';
  const cell=row.cells[index];return cell?.dataset.sortValue??searchable(row,index,state);
 }
 function searchable(row,index,state) {
  if(state.cards)return value(row,index,state);
  let cached=searchCache.get(row);if(!cached){cached=[];searchCache.set(row,cached)}
  if(cached[index]!==undefined)return cached[index];
  if(row.cells[index]?.dataset.filterValue)return cached[index]=row.cells[index].dataset.filterValue;
  const cell=row.cells[index]?.cloneNode(true);cell?.querySelectorAll('.speech-tooltip,[style*="display:none"],[style*="display: none"]').forEach(n=>n.remove());return cached[index]=text(cell);
 }
 function apply(state) {
  let rows=state.rows.filter(row=>state.filters.every((f,i)=>!f||searchable(row,i,state).toLowerCase().includes(f.toLowerCase())) && (!state.zone || row.dataset.zone===state.zone));
  if(state.sort!==null) rows.sort((a,b)=>{
   const x=value(a,state.sort,state),y=value(b,state.sort,state);
   const numeric=state.numeric.has(state.sort);
   if(state.addressColumns.has(state.sort)){const ax=addressKey(x),ay=addressKey(y);return state.direction*(ax<ay?-1:ax>ay?1:0)}
   return state.direction*(numeric?Number(x)-Number(y):collator.compare(x,y));
  });
  const pages=Math.max(1,Math.ceil(rows.length/state.size));state.page=Math.min(state.page,pages-1);
  const visible=rows.slice(state.page*state.size,(state.page+1)*state.size);
  visible.forEach(r=>r.hidden=false);state.body.replaceChildren(...visible);
  state.summary.textContent=`${rows.length ? state.page*state.size+1 : 0}–${Math.min(rows.length,(state.page+1)*state.size)} of ${rows.length}`;
  state.prev.disabled=state.page===0;state.next.disabled=state.page>=pages-1;state.empty.hidden=rows.length!==0;
  state.sortButtons.forEach((b,i)=>{b.textContent=state.sort===i?(state.direction===1?'↑':'↓'):'↕';b.closest('th')?.setAttribute('aria-sort',state.sort===i?(state.direction===1?'ascending':'descending'):'none');});
 }
 function controls(state) {
  const bar=document.createElement('div');bar.className='list-controls';state.controls=bar;
  const summary=document.createElement('span');summary.className='list-summary';summary.setAttribute('role','status');state.summary=summary;
  const label=document.createElement('label');label.textContent='Rows ';label.className='mb-0';
  const size=document.createElement('select');size.className='custom-select custom-select-sm';size.setAttribute('aria-label','Entries per page');
  [25,50,100,250].forEach(n=>size.add(new Option(n,n)));label.append(size);
  size.addEventListener('change',()=>{state.size=Number(size.value);state.page=0;apply(state)});
  state.prev=button('‹','Previous page');state.next=button('›','Next page');
  state.prev.onclick=()=>{state.page--;apply(state)};state.next.onclick=()=>{state.page++;apply(state)};
  const reset=button('↶','Clear filters and restore original order');reset.onclick=()=>{
   state.filters.fill('');state.root.querySelectorAll('.list-filter-input').forEach(i=>i.value='');state.sort=null;state.page=0;state.zone='';
   const z=document.querySelector('.entry-zone-filter');if(z)z.value='';apply(state);
  };
  const refresh=button('⟳','Refresh list');refresh.onclick=()=>refreshLists();
  const status=document.createElement('span');status.className='list-state';status.setAttribute('role','status');state.status=status;
  bar.append(summary,label,state.prev,state.next,reset,refresh,status);return bar;
 }
 function fieldControl(state,index,title,holder,clock) {
  const heading=document.createElement('div');heading.className='list-heading';
  const toggle=button(title,`Filter ${title}`);toggle.className='list-filter-toggle';toggle.title=`Filter ${title}`;
  if(clock){toggle.replaceChildren(clock.cloneNode(true));toggle.classList.add('entry-time-sort');toggle.title=holder.title;}
  const sort=button('↕',`Sort ${title}`);sort.className='list-sort';state.sortButtons[index]=sort;
  const input=document.createElement('input');input.type='search';input.className='form-control form-control-sm list-filter-input';input.placeholder=`Filter ${title}`;input.setAttribute('aria-label',`Filter ${title}`);input.hidden=true;
  toggle.setAttribute('aria-expanded','false');toggle.onclick=()=>{input.hidden=!input.hidden;toggle.setAttribute('aria-expanded',String(!input.hidden));if(!input.hidden)input.focus();};
  input.oninput=()=>{state.filters[index]=input.value;state.page=0;apply(state)};
  input.onkeydown=e=>{if(e.key==='Escape'){input.value='';state.filters[index]='';input.hidden=true;toggle.setAttribute('aria-expanded','false');apply(state);toggle.focus();}};
  sort.onclick=()=>{state.direction=state.sort===index?-state.direction:1;state.sort=index;state.page=0;apply(state)};
  heading.append(toggle,sort);holder.append(heading,input);
 }
 tables.forEach((table,index)=>{
  table.classList.add('list-table');table.dataset.liveIndex=index;
  const root=document.createElement('section');root.className='list-widget';table.before(root);root.append(table);
  const state={root,table,body:table.tBodies[0],rows:[...table.tBodies[0].rows],filters:[],sort:null,direction:1,page:0,size:25,sortButtons:[],numeric:new Set(),addressColumns:new Set(),cards:false,index};
  [...table.tHead.rows[0].cells].forEach((th,i)=>{
   state.filters[i]='';const title=th.querySelector('.entry-sort')?.getAttribute('aria-label')?.startsWith('Sort by last')?'Last update':text(th)||'Actions';
   if(/Actions|Password|Cert$/.test(title))return;
   if(/^(IP|IP sent|Caller IP|IP Address)$/.test(title))state.addressColumns.add(i);
   if(/TTL|Repeats|Failures|Last update|🕒/.test(title))state.numeric.add(i);
   const clock=th.querySelector('.entry-time-sort svg');if(clock)th.title=th.querySelector('.entry-time-sort').title;
   th.replaceChildren();fieldControl(state,i,title,th,clock);
  });
  root.prepend(controls(state));state.empty=document.createElement('p');state.empty.className='list-empty';state.empty.textContent='No matching entries.';root.append(state.empty);states.push(state);apply(state);
 });
 if(cards) {
  const root=document.createElement('section');root.className='list-widget live-cards';cards.before(root);root.append(cards);
  const fields=(cards.dataset.fields||'name,status').split(',');const labels=(cards.dataset.labels||'Hostname,Status').split(',');
  const state={root,body:cards,rows:[...cards.children].filter(n=>n.matches('[data-name]')),fields,filters:fields.map(()=>''),sort:null,direction:1,page:0,size:25,sortButtons:[],numeric:new Set(fields.map((f,i)=>f==='ttl'?i:-1)),cards:true,addressColumns:new Set()};
  const filters=document.createElement('div');filters.className='card-filters';labels.forEach((label,i)=>{const box=document.createElement('div');fieldControl(state,i,label,box);filters.append(box)});
  root.prepend(controls(state),filters);state.empty=document.createElement('p');state.empty.className='list-empty';state.empty.textContent='No matching entries.';root.append(state.empty);states.push(state);apply(state);
 }
 const zone=document.querySelector('.entry-zone-filter');if(zone)zone.onchange=()=>{states[0].zone=zone.value;states[0].page=0;apply(states[0])};
 const oldReset=document.querySelector('.entry-sort-reset');if(oldReset){oldReset.disabled=false;oldReset.onclick=()=>{states[0].sort=null;states[0].page=0;apply(states[0])};}
 let busy=false;const dirtyForms=new Set();let submitting=false;
 page.addEventListener('input',e=>{if(e.target.closest('form')&&!e.target.classList.contains('list-filter-input'))dirtyForms.add(e.target.closest('form'))});
 page.addEventListener('change',e=>{if(e.target.closest('form')&&!e.target.closest('.list-controls'))dirtyForms.add(e.target.closest('form'))});
 async function refreshLists() {
  if(busy||submitting||document.hidden)return;
  if(dirtyForms.size||document.activeElement?.closest('form')||page.querySelector('.speech-tooltip.active')){states.forEach(s=>s.status.textContent='Updates paused while editing');return;}
  busy=true;const controller=new AbortController();const timer=setTimeout(()=>controller.abort(),10000);
  states.forEach(s=>{s.status.textContent='Updating…';s.status.classList.remove('list-refresh-error')});
  try {
   const response=await fetch(location.href,{credentials:'same-origin',cache:'no-store',redirect:'error',signal:controller.signal});
   if(!response.ok)throw Error('unavailable');const doc=new DOMParser().parseFromString(await response.text(),'text/html');
   const replacement=doc.querySelector('.marketing');if(!replacement||doc.querySelector('#login-form'))throw Error('session');
   const newTables=[...replacement.querySelectorAll('table')];
   for(const state of states){
    const source=state.cards?replacement.querySelector('.live-items'):newTables[state.index]?.tBodies[0];if(!source)throw Error('markup');
    const nodes=[...source.children].filter(n=>!state.cards||n.matches('[data-name]')).map(n=>document.importNode(n,true));
    state.rows=nodes;apply(state);state.status.textContent='Live · '+new Date().toLocaleTimeString();
   }
   const warning=page.querySelector('.proxy-warning'),newWarning=replacement.querySelector('.proxy-warning');if(warning&&newWarning){warning.textContent=newWarning.textContent;warning.hidden=newWarning.hidden;}
   const currentSummary=page.querySelector('.security-summary');const newSummary=replacement.querySelector('.security-summary');if(currentSummary&&newSummary)currentSummary.replaceChildren(...[...newSummary.children].map(n=>document.importNode(n,true)));
  }catch(e){states.forEach(s=>{s.status.textContent='Update unavailable — retrying';s.status.classList.add('list-refresh-error')})}
  finally {clearTimeout(timer);busy=false;}
 }
 window.refreshLists=refreshLists;
 // AJAX mutations use the existing server validation and CSRF tokens. No edits are discarded by polling.
 page.addEventListener('submit',async e=>{
  const form=e.target;if(!form.matches('form[method="post"]')||!/^\/@\/(records|certs)/.test(form.getAttribute('action')||''))return;
  e.preventDefault();if(submitting)return;submitting=true;
  const data=new FormData(form);if(e.submitter?.name)data.append(e.submitter.name,e.submitter.value);
  const message=document.createElement('p');message.setAttribute('role','status');message.textContent='Saving…';form.append(message);
  const submit=e.submitter;if(submit)submit.disabled=true;
  try{
   const response=await fetch(form.action,{method:'POST',body:new URLSearchParams(data),credentials:'same-origin',headers:{Accept:'text/html'}});
   if(!response.ok||new URL(response.url).pathname.includes('/login'))throw Error('Save failed. Check your inputs and sign-in, then retry.');
   message.textContent='Saved';dirtyForms.delete(form);
  }catch(error){message.textContent=error.message;message.className='text-danger';}
  finally{submitting=false;if(submit)submit.disabled=false;}
  if(!dirtyForms.size){document.activeElement?.blur();await refreshLists();}
 });
 const refreshLink=page.querySelector('a[href="/@/certs"].btn');if(refreshLink)refreshLink.onclick=e=>{e.preventDefault();refreshLists()};
 setInterval(refreshLists,10000);
})();
