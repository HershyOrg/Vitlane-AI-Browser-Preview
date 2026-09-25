#!/usr/bin/env python3
"""Collect completed GitHub Actions attempt/job/step timings through authenticated gh."""
import argparse
import datetime as dt
import json
import math
import statistics
import subprocess


def api(path):
    return json.loads(subprocess.check_output(['gh', 'api', path], text=True))


def seconds(start, end):
    parse = lambda value: dt.datetime.fromisoformat(value.replace('Z', '+00:00'))
    return round((parse(end) - parse(start)).total_seconds(), 3)


def collect(repo, spec):
    run_id, separator, attempt = spec.partition(':')
    base = f'repos/{repo}/actions/runs/{int(run_id)}'
    run = api(base + (f'/attempts/{int(attempt)}' if separator else ''))
    if run['status'] != 'completed':
        raise ValueError(f'{spec} is not completed')
    attempt = run['run_attempt']
    jobs = []
    page = 1
    while True:
        data = api(f'{base}/attempts/{attempt}/jobs?per_page=100&page={page}')
        jobs.extend(data['jobs'])
        if len(jobs) >= data['total_count']:
            break
        if not data['jobs']:
            raise ValueError('Incomplete job inventory')
        page += 1
    measured = []
    for job in jobs:
        measured.append(dict(name=job['name'], id=job['id'], result=job['conclusion'],
            seconds=seconds(job['started_at'], job['completed_at']),
            steps=[dict(name=s['name'], result=s['conclusion'],
                        seconds=seconds(s['started_at'], s['completed_at']))
                   for s in job.get('steps', []) if s.get('started_at') and s.get('completed_at')]))
    return dict(id=run['id'], attempt=attempt, sha=run['head_sha'], event=run['event'],
                branch=run['head_branch'], result=run['conclusion'], url=run['html_url'],
                started_at=run['run_started_at'], completed_at=run['updated_at'],
                seconds=seconds(run['run_started_at'], run['updated_at']),
                initial_created_to_completed_seconds=seconds(run['created_at'], run['updated_at']) if attempt == 1 else None,
                initial_queue_seconds=seconds(run['created_at'], run['run_started_at']) if attempt == 1 else None,
                runner_seconds=round(sum(j['seconds'] for j in measured), 3), jobs=measured)


def summarize(runs):
    values = sorted(r['seconds'] for r in runs if r['result'] == 'success')
    if not values:
        return {'n': 0}
    return dict(n=len(values), median_seconds=statistics.median(values), min_seconds=min(values),
                max_seconds=max(values), p95_nearest_rank_seconds=values[math.ceil(len(values)*.95)-1])


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--repo', required=True)
    parser.add_argument('runs', nargs='+', help='RUN_ID or RUN_ID:ATTEMPT')
    args = parser.parse_args()
    runs = [collect(args.repo, spec) for spec in args.runs]
    print(json.dumps({'runs': runs, 'successful_attempts': summarize(runs)}, ensure_ascii=False, indent=2))
