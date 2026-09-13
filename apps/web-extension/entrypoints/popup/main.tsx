import { createRoot } from 'react-dom/client';
import '@gh-stories/ui/styles.css';
import './popup.css';
import { App } from './App.js';

const el = document.getElementById('root');
if (el) createRoot(el).render(<App />);
