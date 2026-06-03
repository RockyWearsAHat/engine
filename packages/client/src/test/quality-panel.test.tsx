import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';
import QualityPanel from '../components/Quality/QualityPanel.js';
import { useStore } from '../store/index.js';

function resetQualityState() {
  useStore.setState({
    qualityReport: null,
    qualityProgress: null,
    qualityLoading: false,
    qualityCompleted: false,
    qualityError: null,
  });
}

describe('QualityPanel', () => {
  beforeEach(() => {
    resetQualityState();
  });

  it('QualityPanel_ScanNotCompleted_ShowsCenteredProgressAndDescription', () => {
    useStore.setState({
      qualityLoading: true,
      qualityCompleted: false,
      qualityProgress: {
        projectPath: '/tmp/p',
        phase: 'scan',
        current: 3,
        total: 12,
        percent: 25,
        currentFile: 'packages/server-go/quality/report.go',
        currentFunction: 'analyzeFile',
        section: 'scan',
      },
    });

    render(<QualityPanel />);

    expect(screen.getByLabelText('quality-scan-largebar')).toBeTruthy();
    expect(screen.getByText(/Scanning project - packages\/server-go\/quality\/report.go\|analyzeFile\(\/\/scan\)/)).toBeTruthy();
  });

  it('QualityPanel_ScanInProgressAfterCompletion_ShowsTinyTopBar', () => {
    useStore.setState({
      qualityLoading: true,
      qualityCompleted: true,
      qualityProgress: {
        projectPath: '/tmp/p',
        phase: 'report',
        current: 40,
        total: 100,
        percent: 40,
      },
    });

    render(<QualityPanel />);

    expect(screen.getByLabelText('quality-scan-topbar')).toBeTruthy();
  });

  it('QualityPanel_ReportSuccess_RendersExplorerGroupsAndIssueStats', () => {
    useStore.setState({
      qualityCompleted: true,
      qualityReport: {
        projectPath: '/tmp/p',
        generatedAt: new Date().toISOString(),
        issueCount: 2,
        highCount: 1,
        mediumCount: 1,
        lowCount: 0,
        issues: [
          {
            id: 'q1',
            severity: 'high',
            category: 'dead-code',
            message: 'Unused function',
            file: 'a.ts',
            line: 3,
            suggestion: 'Delete it',
          },
          {
            id: 'q2',
            severity: 'medium',
            category: 'documentation-gap',
            message: 'Missing docs',
            file: 'b.ts',
            line: 8,
          },
        ],
      },
    });

    render(<QualityPanel />);

    expect(screen.getByText('Total: 2')).toBeTruthy();
    expect(screen.getByText('High: 1')).toBeTruthy();
    expect(screen.getByText('Medium: 1')).toBeTruthy();
    expect(screen.getByText('Low: 0')).toBeTruthy();
    expect(screen.getByText('Unused function')).toBeTruthy();
    expect(screen.getByText('Delete it')).toBeTruthy();
    expect(screen.getByText('Missing docs')).toBeTruthy();
    expect(screen.getByText('a.ts (1)')).toBeTruthy();
    expect(screen.getByText('b.ts (1)')).toBeTruthy();
  });

  it('QualityPanel_Error_ShowsErrorMessage', () => {
    useStore.setState({ qualityError: 'scan failed' });
    render(<QualityPanel />);
    expect(screen.getByText('scan failed')).toBeTruthy();
  });
});
