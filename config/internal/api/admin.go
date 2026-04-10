package api

import "net/http"

func (s *Server) handleAdminUI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	u, p, ok := r.BasicAuth()
	if s.adminUser != "" && (!ok || u != s.adminUser || p != s.adminPass) {
		w.Header().Set("WWW-Authenticate", `Basic realm="config admin"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(adminHTML))
}

const adminHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>SmurovProxy Config</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,system-ui,sans-serif;background:#0b0f1a;color:#e2e8f0;padding:24px;max-width:800px;margin:0 auto}
h1{font-size:20px;margin-bottom:16px}
.tabs{display:flex;gap:4px;margin-bottom:20px}
.tab{padding:8px 16px;background:#1a2234;border:1px solid #2a3a5a;border-radius:6px;color:#94a3b8;cursor:pointer;font-size:14px}
.tab.active{background:#1e3a5f;color:#fff;border-color:#3b82f6}
.panel{display:none}.panel.active{display:block}
table{width:100%;border-collapse:collapse;margin-bottom:16px}
th,td{text-align:left;padding:8px 12px;border-bottom:1px solid #1e2533;font-size:13px}
th{color:#64748b;font-weight:600}
.badge{display:inline-block;padding:2px 8px;border-radius:10px;font-size:11px;font-weight:600}
.badge.update{background:#1e3a5f;color:#60a5fa}
.badge.migration{background:#3b1f1f;color:#f87171}
.badge.maintenance{background:#2d2006;color:#fbbf24}
.badge.info{background:#1a2234;color:#94a3b8}
.badge.active{background:#0f3d1a;color:#4ade80}
.badge.inactive{background:#1a2234;color:#64748b}
input,textarea,select{background:#0f1420;border:1px solid #2a3a5a;color:#e2e8f0;padding:6px 10px;border-radius:4px;font-size:13px;width:100%}
textarea{min-height:60px;resize:vertical}
button{padding:6px 14px;border:none;border-radius:4px;font-size:13px;cursor:pointer;font-weight:500}
.btn-primary{background:#3b82f6;color:#fff}
.btn-danger{background:#ef4444;color:#fff}
.btn-sm{padding:3px 8px;font-size:12px}
.form-row{display:flex;gap:8px;margin-bottom:8px;align-items:end}
.form-group{flex:1}
.form-group label{display:block;font-size:12px;color:#64748b;margin-bottom:4px}
.mt{margin-top:16px}
</style>
</head>
<body>
<h1>SmurovProxy Config</h1>
<div class="tabs">
  <div class="tab active" onclick="showTab('notifs')">Notifications</div>
  <div class="tab" onclick="showTab('services')">Services</div>
</div>
<div id="notifs" class="panel active"></div>
<div id="services" class="panel"></div>
<script>
const API = '';
function showTab(id) {
  document.querySelectorAll('.tab').forEach(t => t.classList.toggle('active', t.textContent.trim() === (id==='notifs'?'Notifications':'Services')));
  document.querySelectorAll('.panel').forEach(p => p.classList.toggle('active', p.id === id));
}
async function api(method, path, body) {
  const opts = {method, headers: {'Content-Type':'application/json'}};
  if (body) opts.body = JSON.stringify(body);
  const r = await fetch(API + path, opts);
  if (r.status === 204) return null;
  return r.json();
}
async function loadNotifs() {
  const data = await api('GET', '/api/admin/notifications');
  let html = '<table><tr><th>Type</th><th>Title</th><th>Status</th><th></th></tr>';
  (data||[]).forEach(n => {
    html += '<tr><td><span class="badge '+n.type+'">'+n.type+'</span></td>';
    html += '<td>'+n.title+'</td>';
    html += '<td><span class="badge '+(n.active?'active':'inactive')+'">'+(n.active?'active':'off')+'</span></td>';
    html += '<td><button class="btn-sm btn-danger" onclick="delNotif(\''+n.id+'\')">Delete</button> ';
    html += '<button class="btn-sm btn-primary" onclick="toggleNotif(\''+n.id+'\','+!n.active+')">'+(n.active?'Disable':'Enable')+'</button></td></tr>';
  });
  html += '</table>';
  html += '<div class="mt"><h3 style="font-size:14px;margin-bottom:8px">Create Notification</h3>';
  html += '<div class="form-row"><div class="form-group"><label>Type</label><select id="n-type"><option>update</option><option>migration</option><option>maintenance</option><option>info</option></select></div>';
  html += '<div class="form-group"><label>Title</label><input id="n-title"></div></div>';
  html += '<div class="form-group" style="margin-bottom:8px"><label>Message</label><textarea id="n-msg"></textarea></div>';
  html += '<button class="btn-primary" onclick="createNotif()">Create</button></div>';
  document.getElementById('notifs').innerHTML = html;
}
async function loadServices() {
  const data = await api('GET', '/api/admin/services');
  let html = '<table><tr><th>Key</th><th>Value</th></tr>';
  Object.entries(data||{}).forEach(([k,v]) => {
    html += '<tr><td>'+k+'</td><td><input id="svc-'+k+'" value="'+v+'"></td></tr>';
  });
  html += '</table><button class="btn-primary" onclick="saveServices()">Save</button>';
  document.getElementById('services').innerHTML = html;
}
async function createNotif() {
  await api('POST', '/api/admin/notifications', {
    type: document.getElementById('n-type').value,
    title: document.getElementById('n-title').value,
    message: document.getElementById('n-msg').value
  });
  loadNotifs();
}
async function delNotif(id) { await api('DELETE', '/api/admin/notifications/'+id); loadNotifs(); }
async function toggleNotif(id, active) { await api('PATCH', '/api/admin/notifications/'+id, {active}); loadNotifs(); }
async function saveServices() {
  const inputs = document.querySelectorAll('[id^="svc-"]');
  const body = {};
  inputs.forEach(i => body[i.id.replace('svc-','')] = i.value);
  await api('PUT', '/api/admin/services', body);
  alert('Saved');
}
loadNotifs(); loadServices();
</script>
</body>
</html>`
