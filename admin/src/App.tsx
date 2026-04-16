import { useState } from "react";
import { BrowserRouter, Routes, Route, Link, useLocation } from "react-router-dom";
import { Dashboard } from "./pages/Dashboard";
import { Users } from "./pages/Users";
import { UserDetail } from "./pages/UserDetail";
import { Sites } from "./pages/Sites";
import { SiteDetail } from "./pages/SiteDetail";
import { Releases } from "./pages/Releases";
import { Changelog } from "./pages/Changelog";
import { Logs } from "./pages/Logs";
import { Notifications } from "./pages/Notifications";
import { getCredentials, setCredentials } from "./lib/auth";

function Login({ onLogin }: { onLogin: () => void }) {
  const [user, setUser] = useState("");
  const [pass, setPass] = useState("");
  const [error, setError] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setCredentials(user, pass);
    const API_URL = import.meta.env.VITE_API_URL || "https://proxyness.smurov.com";
    try {
      const res = await fetch(`${API_URL}/admin/api/stats/overview`, {
        headers: { Authorization: `Basic ${btoa(`${user}:${pass}`)}` },
      });
      if (res.ok) {
        onLogin();
      } else {
        setError(true);
      }
    } catch {
      setError(true);
    }
  };

  return (
    <div className="min-h-screen bg-background text-foreground flex items-center justify-center">
      <form onSubmit={submit} className="w-80 space-y-4">
        <h1 className="text-2xl font-bold text-center">Proxyness Admin</h1>
        <input
          type="text"
          placeholder="Username"
          value={user}
          onChange={(e) => setUser(e.target.value)}
          className="w-full px-3 py-2 border rounded-md bg-background"
          autoFocus
        />
        <input
          type="password"
          placeholder="Password"
          value={pass}
          onChange={(e) => setPass(e.target.value)}
          className="w-full px-3 py-2 border rounded-md bg-background"
        />
        {error && <p className="text-red-500 text-sm">Invalid credentials</p>}
        <button type="submit" className="w-full px-3 py-2 bg-primary text-primary-foreground rounded-md">
          Sign in
        </button>
      </form>
    </div>
  );
}

function Nav() {
  const loc = useLocation();
  const link = (to: string, label: string) => (
    <Link
      to={to}
      className={`px-3 py-2 rounded-md text-sm font-medium ${
        loc.pathname === to ? "bg-secondary text-secondary-foreground" : "text-muted-foreground hover:text-foreground"
      }`}
    >
      {label}
    </Link>
  );
  return (
    <nav className="border-b px-6 py-3 flex items-center gap-4">
      <span className="font-bold text-lg mr-4">Proxyness</span>
      {link("/", "Dashboard")}
      {link("/users", "Users")}
      {link("/sites", "Sites")}
      {link("/notifications", "Notifications")}
      {link("/releases", "Releases")}
      {link("/changelog", "Changelog")}
      {link("/logs", "Logs")}
    </nav>
  );
}

export default function App() {
  const [authed, setAuthed] = useState(!!getCredentials());

  if (!authed) return <Login onLogin={() => setAuthed(true)} />;

  return (
    <BrowserRouter>
      <div className="min-h-screen bg-background text-foreground">
        <Nav />
        <main className="p-6 max-w-5xl mx-auto">
          <Routes>
            <Route path="/" element={<Dashboard />} />
            <Route path="/users" element={<Users />} />
            <Route path="/users/:id" element={<UserDetail />} />
            <Route path="/sites" element={<Sites />} />
            <Route path="/sites/:id" element={<SiteDetail />} />
            <Route path="/notifications" element={<Notifications />} />
            <Route path="/releases" element={<Releases />} />
            <Route path="/changelog" element={<Changelog />} />
            <Route path="/logs" element={<Logs />} />
          </Routes>
        </main>
      </div>
    </BrowserRouter>
  );
}
