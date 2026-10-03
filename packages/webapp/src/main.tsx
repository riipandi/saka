import ReactDOM from 'react-dom/client'
import App from './app'
import './app.css'

// Create root element and ensure exists. React clears the static shell on first commit.
const rootElement = document.getElementById('root')
if (!rootElement) {
  throw new Error('Root element missing. Verify the element exists and the ID is correct.')
}

const root = ReactDOM.createRoot(rootElement)
root.render(<App />)
