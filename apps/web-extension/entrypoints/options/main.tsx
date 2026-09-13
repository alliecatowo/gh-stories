import { createRoot } from 'react-dom/client';
import '@gh-stories/ui/styles.css';
import { OptionsApp } from './OptionsApp.js';

const el = document.getElementById('root');
if (el) createRoot(el).render(<OptionsApp />);
