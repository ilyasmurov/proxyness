import { BrowserRouter, Routes, Route, Link, useLocation } from "react-router-dom";
import { Dashboard } from "./pages/Dashboard";
import { Users } from "./pages/Users";
import { UserDetail } from "./pages/UserDetail";
import { Releases } from "./pages/Releases";
import { Changelog } from "./pages/Changelog";

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
      <span className="font-bold text-lg mr-4">SmurovProxy</span>
      {link("/admin", "Dashboard")}
      {link("/admin/users", "Users")}
      {link("/admin/releases", "Releases")}
      {link("/admin/changelog", "Changelog")}
    </nav>
  );
}

export default function App() {
  return (
    <BrowserRouter>
      <div className="min-h-screen bg-background text-foreground">
        <Nav />
        <main className="p-6 max-w-5xl mx-auto">
          <Routes>
            <Route path="/admin" element={<Dashboard />} />
            <Route path="/admin/users" element={<Users />} />
            <Route path="/admin/users/:id" element={<UserDetail />} />
            <Route path="/admin/releases" element={<Releases />} />
            <Route path="/admin/changelog" element={<Changelog />} />
          </Routes>
        </main>
      </div>
    </BrowserRouter>
  );
}
