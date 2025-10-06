// src/App.tsx
import React, {useState} from 'react';

function App() {
    const [longUrl, setLongUrl] = useState('');
    const [shortUrl, setShortUrl] = useState('');
    const [error, setError] = useState('');

    const handleShorten = async () => {

    };

    return (
        <div className='App'>
            <h1>URL Shortener</h1>
            <input 
                type="text"
                value={longUrl}
                onChange={(e) => setLongUrl(e.target.value)}
                placeholder='Enter Long URL' 
            />
            <button onClick={handleShorten}>Shorten</button>

        </div>
    );
}

export default App;