# input: 构建前端、Playwright/Chromium 和合成离线批次 API。
# output: 批次创建启动、逐版本评分、草稿晋升和移动端截图验证。
# pos: 离线实验的精简浏览器验收。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
import copy
import functools
import http.server
import json
import os
from pathlib import Path
import threading
from urllib.parse import urlparse
from playwright.sync_api import sync_playwright, expect

root = Path(__file__).resolve().parents[1]
artifacts = root / '.cache/lab-ui'
artifacts.mkdir(parents=True, exist_ok=True)
status = {'sync': {'department_id': '7', 'selected': 5, 'ready': 5, 'failed': 0}, 'runtime': {'automatic': True, 'last_error': {}}, 'bub_enabled': True, 'jev_enabled': False, 'csrf_token': 'synthetic', 'budget': {'total': 0, 'round': 0}, 'ai': {}}
message = {'id': 'c', 'source': 'thread', 'role': 'customer', 'visibility': 'public', 'created_time': '2026-11-06T01:00:00Z', 'body': 'The printer still prints labels with an offset. What should I check?'}
samples = [{'ticket_id': str(i), 'ticket_number': str(300+i), 'subject': 'Printer calibration and media alignment', 'group': str(i), 'split': 'development' if i == 1 else 'holdout' if i == 2 else 'learn', 'snapshot': {'messages': [message]}, 'nodes': [{'id': f'{i}:c', 'cutoff_id': 'c', 'cutoff_time': message['created_time'], 'label': '首次来信', 'ticket': {'ticket_id': str(i), 'messages': [message]}}]} for i in range(1,6)]
entry = {'id': 10, 'kind': 'sop', 'scope': 'technical', 'status': 'published', 'title': '打印偏移排查', 'product': '', 'source_name': 'synthetic', 'source_hash': 'synthetic', 'source_tickets': [], 'message_ids': [], 'blocks': [{'id': 'faq', 'text': '确认耗材条件，再核对校准步骤。'}], 'created_at': message['created_time']}
candidate = {'active': True, 'key': '1:0', 'ticket_id': '3', 'entry': entry, 'proposal': {'title': entry['title'], 'product': '', 'question': '如何排查偏移', 'answer': '确认耗材适用条件', 'conditions': '型号及耗材需核对', 'reason': '补齐排查前提', 'replaces_id': 0, 'message_ids': ['c'], 'source_ids': []}}
batches = {}
posts = []

def complete(batch):
    calls = []
    for sample in samples[:2]:
        for variant in (['candidate'] if sample['split'] == 'development' else ['baseline', 'candidate']):
            node = sample['nodes'][0]
            for task in ['reply', 'evaluate']:
                result = {'text': 'Please confirm the media type before calibration.', 'language': 'en', 'rationale': '先核对耗材条件。', 'missing_information': ['耗材类型'], 'source_ids': [], 'message_ids': ['c']} if task == 'reply' else {'verdict': 'pass', 'score': 4, 'findings': [], 'suggestions': []}
                calls.append({'id': len(calls)+1, 'key': f"{node['id']}:{variant}:0:{task}", 'task': task, 'node_id': node['id'], 'variant': variant, 'revision': 0, 'status': 'success', 'started_at': message['created_time'], 'completed_at': message['created_time'], 'input_hash': 'synthetic', 'input': {'ticket': node['ticket'], 'sources': []}, 'result': result})
    batch['state'].update(status='completed', phase='review', calls=calls)

class QuietHandler(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *_):
        pass

server = http.server.ThreadingHTTPServer(('127.0.0.1',0), functools.partial(QuietHandler, directory=str(root/'web/dist')))
threading.Thread(target=server.serve_forever, daemon=True).start()
with sync_playwright() as playwright:
    configured = os.getenv('CHROMIUM_PATH')
    cached = sorted((Path.home()/'.cache/ms-playwright').glob('chromium-*/chrome-linux64/chrome'))
    launch = {'executable_path': configured or str(cached[-1])} if configured or cached else {}
    browser = playwright.chromium.launch(**launch)
    page = browser.new_page(viewport={'width':1366,'height':900}, reduced_motion='reduce')
    errors=[]
    page.on('pageerror',lambda error: errors.append(str(error)))
    def route(request):
        path=urlparse(request.request.url).path
        if request.request.method=='POST':
            assert request.request.headers.get('x-csrf-token')=='synthetic'
            body=json.loads(request.request.post_data or '{}');posts.append((path,body))
            if path=='/api/lab/batches':
                batch={'id':len(batches)+1,'revision':1,'created_at':message['created_time'],'spec':{'schema':'bub-lab-v1','model':'synthetic','start_time':'2026-11-05T00:00:00+08:00','end_time':'2026-11-09T00:00:00Z','samples':copy.deepcopy(samples),'knowledge':[entry]},'state':{'status':'draft','phase':'learning','calls':[],'feedback':[],'drafts':{}}}
                batches[batch['id']]=batch;data=batch
            else:
                batch=batches[int(path.split('/')[4])];batch['revision']+=1
                if path.endswith('/start'):complete(batch);data={'status':'running'}
                elif path.endswith('/feedback'):
                    data={**body,'id':1,'node_id':'1:c','created_at':message['created_time']};batch['state']['feedback'].append(data)
                elif path.endswith('/drafts'):batch['state']['drafts']['1:0']=20;data={**entry,'id':20,'status':'draft'}
                else:raise AssertionError(path)
        elif path=='/api/status':data=status
        elif path=='/api/lab/batches':data={'data':list(batches.values())}
        elif path=='/api/lab/candidates':data={'data':[{'id':s['ticket_id'],'ticket_number':s['ticket_number'],'subject':s['subject'],'created_time':message['created_time'],'nodes':1} for s in samples],'from':'2026-11-05T00:00:00+08:00','until':'2026-11-09T00:00:00Z'}
        elif path.startswith('/api/lab/batches/'):
            batch=batches[int(path.rsplit('/',1)[1])];data={'batch':batch,'candidates':[candidate] if batch['state']['status']=='completed' else [],'validation_complete':batch['state']['status']=='completed'}
        elif path=='/api/knowledge':data={'data':[entry]}
        else:raise AssertionError(path)
        request.fulfill(status=200,json=data)
    page.route('**/api/**',route)
    page.goto(f'http://127.0.0.1:{server.server_port}/#/lab')
    page.get_by_role('button',name='选择本地历史工单').click()
    expect(page.get_by_role('heading',name='本批输入 · 已选 5 张')).to_be_visible()
    page.get_by_role('button',name='保存批次并预览').click()
    expect(page.get_by_role('heading',name='离线批次 #1')).to_be_visible()
    assert not any(path.endswith('/start') for path,_ in posts)
    page.get_by_role('button',name='启动本批模拟').click()
    expect(page.get_by_text('Please confirm the media type before calibration.',exact=True)).to_be_visible()
    page.get_by_text('人工评分与修改',exact=True).click()
    page.get_by_label('修改建议（可选）').fill('先问耗材规格，避免重复失败步骤')
    page.wait_for_timeout(5500)
    expect(page.get_by_label('修改建议（可选）')).to_have_value('先问耗材规格，避免重复失败步骤')
    page.get_by_role('button',name='保存评分').click()
    expect(page.get_by_text('评分已保存；开发样本的反馈在下一轮使用，留出样本仅供审核。')).to_be_visible()
    page.get_by_text('打印偏移排查 · 新增',exact=True).click()
    page.get_by_role('button',name='保存为修订草稿').click()
    expect(page.get_by_role('link',name='查看草稿并审核')).to_have_attribute('href','#/knowledge/20')
    page.screenshot(path=str(artifacts/'review-desktop.png'),full_page=True)
    for width in [390,320]:
        page.set_viewport_size({'width':width,'height':900})
        assert page.evaluate('document.documentElement.scrollWidth <= innerWidth'), f'overflow at {width}'
    page.screenshot(path=str(artifacts/'review-mobile.png'),full_page=True)
    page.get_by_role('button',name='带入开发反馈，创建下一轮').click()
    expect(page.get_by_role('heading',name='离线批次 #2')).to_be_visible()
    assert posts[-1][1]=={'parent_id':1}
    assert not errors,errors
    browser.close()
server.shutdown()
print('offline lab UI: PASS')
