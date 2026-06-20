/// OK Agent — Flutter mobile app.
///
/// Connects to an OK Agent via HCP WebSocket protocol.
/// First screen: server list → select server → chat.
library;

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import 'services/connection_manager.dart';
import 'screens/server_list_screen.dart';

void main() {
  runApp(const OkAgentApp());
}

class OkAgentApp extends StatelessWidget {
  const OkAgentApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MultiProvider(
      providers: [
        ChangeNotifierProvider(create: (_) => ConnectionManager()),
      ],
      child: MaterialApp(
        title: "OK Agent",
        debugShowCheckedModeBanner: false,
        theme: _buildLightTheme(),
        darkTheme: _buildDarkTheme(),
        themeMode: ThemeMode.system,
        home: const ServerListScreen(),
      ),
    );
  }

  ThemeData _buildLightTheme() {
    final colorScheme = ColorScheme.fromSeed(
      seedColor: const Color(0xFF4F46E5),
      brightness: Brightness.light,
    );
    return ThemeData(
      useMaterial3: true,
      colorScheme: colorScheme,
      appBarTheme: AppBarTheme(
        centerTitle: true,
        elevation: 0,
        backgroundColor: colorScheme.surface,
        foregroundColor: colorScheme.onSurface,
      ),
    );
  }

  ThemeData _buildDarkTheme() {
    final colorScheme = ColorScheme.fromSeed(
      seedColor: const Color(0xFF818CF8),
      brightness: Brightness.dark,
    );
    return ThemeData(
      useMaterial3: true,
      colorScheme: colorScheme,
      appBarTheme: AppBarTheme(
        centerTitle: true,
        elevation: 0,
        backgroundColor: colorScheme.surface,
        foregroundColor: colorScheme.onSurface,
      ),
    );
  }
}
