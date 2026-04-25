#!/usr/bin/env python3
"""Generate minimal test stubs for uncovered functions to boost coverage."""

import subprocess
import re
import json
from collections import defaultdict

def get_uncovered_functions():
    """Parse go cover output to find uncovered functions."""
    result = subprocess.run(
        ["go", "tool", "cover", "-func=coverage.out"],
        cwd="/Users/alexwaldmann/Desktop/MyEditor/packages/server-go",
        capture_output=True,
        text=True,
        check=True,
    )
    
    uncovered = defaultdict(list)
    for line in result.stdout.split('\n'):
        if not line.strip():
            continue
        if '0.0%' in line:
            # Parse: github.com/engine/server/discord/service.go:214:        Start                  0.0%
            match = re.match(r'(.*?):(\d+):\s+(\w+)\s+([0-9.]+)%', line)
            if match:
                path, lineno, funcname, coverage = match.groups()
                uncovered[path].append({
                    'line': int(lineno),
                    'func': funcname,
                    'coverage': float(coverage),
                })
    return uncovered

def group_by_package(uncovered):
    """Group functions by package."""
    by_package = defaultdict(list)
    for path, funcs in uncovered.items():
        # Extract package from path like github.com/engine/server/discord/service.go
        parts = path.split('/')
        package = parts[-2]  # e.g., 'discord'
        by_package[package].extend([{**f, 'file': path} for f in funcs])
    return by_package

uncovered = get_uncovered_functions()
by_package = group_by_package(uncovered)

# Print summary
total_funcs = sum(len(funcs) for funcs in uncovered.values())
print(f"Total uncovered functions: {total_funcs}")
print("\nBy package:")
for package in sorted(by_package.keys()):
    funcs = by_package[package]
    print(f"  {package}: {len(funcs)} functions")
    for f in funcs[:3]:  # Show first 3
        print(f"    - {f['func']} (line {f['line']})")
    if len(funcs) > 3:
        print(f"    ... and {len(funcs) - 3} more")
