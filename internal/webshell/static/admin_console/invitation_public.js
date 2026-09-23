(() => {
  const messages={paused:'邀请已暂停',full:'当前群已满，请稍后再试',stale:'群信息正在更新，请稍后再试',preparing:'入群方式正在准备，请稍后再试',active:'长按下方二维码加入群聊'};
  async function refresh(){
    const image=document.getElementById('groupCode');
    try{const r=await fetch(location.pathname+'?format=json',{cache:'no-store'});if(!r.ok)throw new Error();const v=await r.json();document.getElementById('title').textContent=v.title;document.getElementById('description').textContent=v.description;document.getElementById('status').textContent=messages[v.state]||'暂时无法入群';image.hidden=v.state!=='active'||!v.qr_code;if(!image.hidden)image.src=v.qr_code;else image.removeAttribute('src');}
    catch{image.hidden=true;image.removeAttribute('src');document.getElementById('status').textContent='暂时无法获取入群方式，请稍后再试';}
    setTimeout(refresh,15000);
  }
  refresh();
})();
