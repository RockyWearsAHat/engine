import { app, BrowserWindow, dialog, ipcMain, Menu, shell } from 'electron';
import path from 'path';
import { spawn, type ChildProcess } from 'child_process';
import fs from 'fs';

const isDev = process.env['NODE_ENV'] !== 'production';
const CLIENT_PORT = 5173;
const SERVER_PORT = 3000;

let mainWindow: BrowserWindow | null = null;
let serverProcess: ChildProcess | null = null;
let chosenProjectPath = '';

function getProjectPath(): string {
  const argPath = process.argv.find((a, i) => i > 1 && !a.startsWith('--') && fs.existsSync(a));
  if (argPath) return argPath;
  return app.getPath('home');
}

function startServer(projectPath: string): Promise<void> {
  return new Promise((resolve, reject) => {
    const serverDir = isDev
      ? path.join(__dirname, '../../server')
      : path.join(process.resourcesPath, 'server');

    const command = isDev ? 'npx' : 'node';
    const args = isDev
      ? ['tsx', 'src/index.ts']
      : ['dist/index.js'];

    const env = {
      ...process.env,
      PORT: String(SERVER_PORT),
      PROJECT_PATH: projectPath,
      NODE_ENV: isDev ? 'development' : 'production',
    };

    console.log(`[desktop] Starting server at ${serverDir} for project ${projectPath}`);

    serverProcess = spawn(command, args, {
      cwd: serverDir,
      env,
      stdio: ['ignore', 'pipe', 'pipe'],
    });

    let started = false;
    const timeout = setTimeout(() => {
      if (!started) reject(new Error('Server failed to start within 10s'));
    }, 10000);

    serverProcess.stdout?.on('data', (data: Buffer) => {
      const text = data.toString();
      console.log('[server]', text.trim());
      if (!started && text.includes('Server running')) {
        started = true;
        clearTimeout(timeout);
        resolve();
      }
    });

    serverProcess.stderr?.on('data', (data: Buffer) => {
      console.error('[server:err]', data.toString().trim());
    });

    serverProcess.on('exit', (code) => {
      console.log(`[server] exited with code ${code}`);
      serverProcess = null;
    });

    // Resolve after a short delay even if startup message wasn't seen
    setTimeout(() => {
      if (!started) {
        started = true;
        clearTimeout(timeout);
        resolve();
      }
    }, 3000);
  });
}

function createWindow(projectPath: string): void {
  mainWindow = new BrowserWindow({
    width: 1400,
    height: 900,
    minWidth: 800,
    minHeight: 600,
    titleBarStyle: 'hiddenInset',
    trafficLightPosition: { x: 14, y: 10 },
    backgroundColor: '#0c0c0e',
    webPreferences: {
      nodeIntegration: false,
      contextIsolation: true,
      preload: path.join(__dirname, 'preload.js'),
    },
    title: `MyEditor — ${path.basename(projectPath)}`,
  });

  if (isDev) {
    mainWindow.loadURL(`http://localhost:${CLIENT_PORT}`);
    mainWindow.webContents.openDevTools({ mode: 'detach' });
  } else {
    const clientDist = path.join(process.resourcesPath, 'client-dist', 'index.html');
    mainWindow.loadFile(clientDist);
  }

  mainWindow.on('closed', () => { mainWindow = null; });
}

function buildMenu(): void {
  const template: Electron.MenuItemConstructorOptions[] = [
    {
      label: 'MyEditor',
      submenu: [
        {
          label: 'Open Folder...',
          accelerator: 'CmdOrCtrl+O',
          click: async () => {
            const result = await dialog.showOpenDialog(mainWindow!, {
              properties: ['openDirectory'],
              title: 'Open Project Folder',
            });
            if (!result.canceled && result.filePaths[0]) {
              await restartWithProject(result.filePaths[0]);
            }
          },
        },
        { type: 'separator' },
        { role: 'quit' },
      ],
    },
    {
      label: 'Edit',
      submenu: [
        { role: 'undo' }, { role: 'redo' }, { type: 'separator' },
        { role: 'cut' }, { role: 'copy' }, { role: 'paste' }, { role: 'selectAll' },
      ],
    },
    {
      label: 'View',
      submenu: [
        { role: 'reload' }, { role: 'forceReload' },
        { type: 'separator' },
        { role: 'resetZoom' }, { role: 'zoomIn' }, { role: 'zoomOut' },
        { type: 'separator' },
        { role: 'togglefullscreen' },
      ],
    },
    {
      label: 'Window',
      submenu: [{ role: 'minimize' }, { role: 'zoom' }, { role: 'close' }],
    },
  ];

  const menu = Menu.buildFromTemplate(template);
  Menu.setApplicationMenu(menu);
}

async function restartWithProject(projectPath: string): Promise<void> {
  if (serverProcess) {
    serverProcess.kill();
    await new Promise(r => setTimeout(r, 500));
  }

  await startServer(projectPath);

  if (mainWindow) {
    mainWindow.setTitle(`MyEditor — ${path.basename(projectPath)}`);
    mainWindow.webContents.reload();
  }
}

async function main(): Promise<void> {
  await app.whenReady();

  let projectPath = getProjectPath();

  if (!process.argv.find((a, i) => i > 1 && !a.startsWith('--'))) {
    const result = await dialog.showOpenDialog({
      properties: ['openDirectory'],
      title: 'Open Project Folder',
      buttonLabel: 'Open with MyEditor',
    });
    if (result.canceled || !result.filePaths[0]) {
      app.quit();
      return;
    }
    projectPath = result.filePaths[0];
  }

  buildMenu();

  chosenProjectPath = projectPath;

  try {
    await startServer(projectPath);
  } catch (err) {
    console.error('[desktop] Server failed to start:', err);
  }

  createWindow(projectPath);

  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow(projectPath);
  });
}

app.on('window-all-closed', () => {
  if (serverProcess) serverProcess.kill();
  if (process.platform !== 'darwin') app.quit();
});

app.on('before-quit', () => {
  if (serverProcess) serverProcess.kill();
});

ipcMain.handle('get-project-path', () => chosenProjectPath || getProjectPath());

const configPath = path.join(app.getPath('userData'), 'config.json');

function loadConfig(): { githubToken?: string } {
  try { return JSON.parse(fs.readFileSync(configPath, 'utf-8')); } catch { return {}; }
}
function saveConfig(data: { githubToken?: string }): void {
  fs.writeFileSync(configPath, JSON.stringify(data, null, 2), 'utf-8');
}

ipcMain.handle('get-github-token', () => loadConfig().githubToken ?? null);
ipcMain.handle('set-github-token', (_event: Electron.IpcMainInvokeEvent, token: string) => {
  saveConfig({ ...loadConfig(), githubToken: token });
  return true;
});
ipcMain.handle('open-external', (_event: Electron.IpcMainInvokeEvent, url: string) => {
  shell.openExternal(url);
});

main().catch(err => {
  console.error('[desktop] Fatal:', err);
  app.quit();
});
