// src/App.tsx
import React, {useState} from 'react';

function App() {
    const [longUrl, setLongUrl] = useState('');
    const [shortUrl, setShortUrl] = useState('');
    const [error, setError] = useState('');

    const handleShorten = async () => {
        try {
            const response = await fetch('/api/v1/shorten', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify({long_url: longUrl}),
            });

            if (!response.ok) {
                throw new Error('Failed to shorten URL');
            }

            const data = await response.json();
            setShortUrl(data.short_url);
            setError('');
        } catch (err) {
            setError("Error shortening URL");
            console.error(err);
        }
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

            {shortUrl && <div>Short URL: <a href={shortUrl}>{shortUrl}</a></div>}
            {error && <div style={{ color: 'red' }}>{error}</div>}
        </div>
    );
}

export default App;