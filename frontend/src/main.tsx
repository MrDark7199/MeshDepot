import { render } from 'solid-js/web'
import App from './App'
import { AuthProvider } from './services/AuthContext'
import { ThemeProvider } from './ThemeContext'
import { I18nProvider } from './i18n/index'

render(
  () => (
    <ThemeProvider>
      <I18nProvider>
        <AuthProvider>
          <App />
        </AuthProvider>
      </I18nProvider>
    </ThemeProvider>
  ),
  document.getElementById('root')!
)
