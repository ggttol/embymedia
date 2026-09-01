#!/usr/bin/env python3
import argparse
import http.client
import os
import pathlib
from urllib.parse import urlsplit


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument('--env', default='/etc/embymedia/caddy.env')
    parser.add_argument('--url-file', default='/run/embymedia/dsh-launch-url')
    parser.add_argument('--authority', default='dsh.gaotao.cc')
    args = parser.parse_args()
    launch_url = pathlib.Path(args.url_file).read_text().strip()
    parsed = urlsplit(launch_url)
    token = parsed.query.removeprefix('token=')
    if (
        parsed.scheme != 'http'
        or parsed.hostname != '127.0.0.1'
        or parsed.port != 3080
        or parsed.path != '/'
        or not parsed.query.startswith('token=')
        or '&' in parsed.query
        or parsed.fragment
        or not token
        or not token.replace('-', '').replace('_', '').isalnum()
    ):
        raise SystemExit('DSH launch URL file is malformed')
    connection = http.client.HTTPConnection('127.0.0.1', 3080, timeout=5)
    connection.request('GET', f'/?token={token}', headers={'Host': args.authority})
    response = connection.getresponse()
    response.read()
    if response.status != 303:
        raise SystemExit(f'DSH token exchange returned HTTP {response.status}')
    set_cookie = response.getheader('Set-Cookie')
    if set_cookie is None:
        raise SystemExit('DSH token exchange returned no cookie')
    cookie = set_cookie.split(';', 1)[0]
    path = pathlib.Path(args.env)
    lines = ['DSH_UPSTREAM_COOKIE=' + cookie]
    temporary = path.with_suffix('.env.tmp')
    temporary.write_text('\n'.join(lines) + '\n')
    os.chmod(temporary, 0o600)
    os.replace(temporary, path)
    print({'env': str(path), 'cookieName': cookie.split('=', 1)[0]})


if __name__ == '__main__':
    main()
