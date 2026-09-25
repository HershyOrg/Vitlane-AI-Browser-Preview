import { StrictMode } from 'react';
import { MemoryRouter } from 'react-router';
import { createRoot } from 'react-dom/client';
import { LocaleProvider } from '../../shared/i18n';
import 'pretendard/dist/web/variable/pretendardvariable.css';
import '../../styles.css';
import '../../product-shell.css';
import { CurationStudio } from './CurationStudio';
import './studio.css';

createRoot(document.getElementById('root')!).render(
  <StrictMode><LocaleProvider><MemoryRouter initialEntries={['/curations/studio']}><CurationStudio /></MemoryRouter></LocaleProvider></StrictMode>,
);
