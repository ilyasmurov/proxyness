package com.smurov.proxy.vpn

import android.content.Intent
import android.net.VpnService
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule
import com.facebook.react.bridge.ReactMethod
import com.facebook.react.bridge.Arguments

class VpnBridgeModule(reactContext: ReactApplicationContext) :
    ReactContextBaseJavaModule(reactContext) {

    override fun getName(): String = "VpnBridge"

    @ReactMethod
    fun connect(server: String, key: String, promise: Promise) {
        val activity = reactApplicationContext.currentActivity
        if (activity == null) {
            promise.reject("NO_ACTIVITY", "No activity")
            return
        }

        val prepareIntent = VpnService.prepare(activity)
        if (prepareIntent != null) {
            promise.reject("VPN_PERMISSION_NEEDED", "User must grant VPN permission first")
            return
        }

        startVpn(server, key, promise)
    }

    @ReactMethod
    fun disconnect(promise: Promise) {
        val intent = Intent(reactApplicationContext, SmurovVpnService::class.java).apply {
            action = SmurovVpnService.ACTION_DISCONNECT
        }
        reactApplicationContext.startService(intent)
        promise.resolve(null)
    }

    @ReactMethod
    fun getStatus(promise: Promise) {
        val map = Arguments.createMap()
        map.putBoolean("connected", SmurovVpnService.isRunning)
        map.putString("error", SmurovVpnService.lastError)
        promise.resolve(map)
    }

    @ReactMethod
    fun requestPermission(promise: Promise) {
        val activity = reactApplicationContext.currentActivity
        if (activity == null) {
            promise.reject("NO_ACTIVITY", "No activity")
            return
        }
        val prepareIntent = VpnService.prepare(activity)
        if (prepareIntent == null) {
            promise.resolve(true)
        } else {
            activity.startActivity(prepareIntent)
            promise.resolve(false)
        }
    }

    private fun startVpn(server: String, key: String, promise: Promise) {
        val intent = Intent(reactApplicationContext, SmurovVpnService::class.java).apply {
            action = SmurovVpnService.ACTION_CONNECT
            putExtra(SmurovVpnService.EXTRA_SERVER, server)
            putExtra(SmurovVpnService.EXTRA_KEY, key)
        }
        reactApplicationContext.startService(intent)
        promise.resolve(null)
    }
}
