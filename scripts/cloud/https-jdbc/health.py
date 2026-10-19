import time,urllib.request
op=urllib.request.build_opener(urllib.request.ProxyHandler({}))
for _ in range(120):
    try:
        for url in ('http://backend:8080/api/ready','http://frontend:5173/'):
            with op.open(url,timeout=2) as r:assert r.status==200
        print('DBHub application ready');break
    except Exception:time.sleep(1)
else:raise SystemExit('DBHub application readiness deadline exceeded')
