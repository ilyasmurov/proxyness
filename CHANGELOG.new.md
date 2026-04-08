## fix
AppRules.tsx: memoize siteDomains/liveSites/enabledSet — без этого applyPac useCallback и re-apply useEffect фаерились на каждом рендере, зовя networksetup десятки раз в секунду, что вешало macOS при запуске клиента
