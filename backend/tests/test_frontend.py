"""Require the production build so missing frontend routes cannot pass silently."""
import re

from fastapi.testclient import TestClient

from app.main import app


def test_homepage_and_production_assets():
    with TestClient(app) as client:
        response = client.get('/')
        assert response.status_code == 200
        assert 'text/html' in response.headers['content-type']
        assert 'id="app"' in response.text
        assets = re.findall(r'(?:src|href)="(/assets/[^"]+)"', response.text)
        assert any(path.endswith('.js') for path in assets)
        assert any(path.endswith('.css') for path in assets)
        for path in assets:
            resource = client.get(path)
            assert resource.status_code == 200
            expected = 'text/javascript' if path.endswith('.js') else 'text/css'
            assert resource.headers['content-type'].split(';')[0] == expected
        assert client.get('/entries').status_code == 200
        assert client.get('/api/does-not-exist').status_code == 404
        assert client.get('/api').status_code == 404
